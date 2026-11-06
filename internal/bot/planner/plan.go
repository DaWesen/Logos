package planner

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"Logos/pkg/strutil"
)

// ==================== Plan-and-Execute 规划器 ====================
//
// 与纯 ReAct 的差异：
//   - ReAct：每一步都要 LLM 决策"下一步做什么"，长任务决策链长、
//     中途漂移无约束，且无法向用户展示"我打算怎么做"；
//   - Plan-and-Execute：先一次规划生成显式步骤图（可展示、可审计），
//     按依赖调度执行，失败或前提被推翻时用 patch 局部修订，最后综合作答。
//
// 每步执行仍复用带工具的 ReAct Agent——单步内的工具调用、
// 知识检索能力完整保留，规划只负责"任务分解"这一层。

// StepStatus 步骤运行状态。
//
// 注意 stale 不能直接进入 running：它必须先回到 pending，重新做前提校验，
// 否则"被推翻的结论"会带着旧前提直接重跑，失效传播就失去意义。
type StepStatus string

const (
	StatusPending StepStatus = "pending"
	StatusReady   StepStatus = "ready"
	StatusRunning StepStatus = "running"
	StatusDone    StepStatus = "done"
	StatusFailed  StepStatus = "failed"  // 永久失败，等待重规划
	StatusBlocked StepStatus = "blocked" // 依赖失败/失效，本轮不可执行
	StatusStale   StepStatus = "stale"   // 上游失效，结果作废，需重跑
	StatusSkipped StepStatus = "skipped" // 被 patch 移除，保留记录用于审计
)

// EffectLevel 声明步骤的副作用等级，决定重放与并行策略。
//
// 这不是给模型看的元信息，是执行器的安全边界：没有它，重规划后的重放
// 可能会把同一条消息发两次、把同一笔钱扣两次。
type EffectLevel string

const (
	// EffectNone 只读：检索、查询、计算。可随意重放、可并行。
	EffectNone EffectLevel = "none"
	// EffectIdempotent 写但幂等：携带幂等键重放不会产生第二条记录。
	EffectIdempotent EffectLevel = "idempotent"
	// EffectSideEffect 不可重放：发消息、发邮件、扣款。重放前必须先查执行记录。
	EffectSideEffect EffectLevel = "side_effect"
)

// PlanStep 计划中的一个步骤。
type PlanStep struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
	// Tool 建议使用的工具名（可选，仅提示，执行器可自行选择）
	Tool string `json:"tool,omitempty"`

	// DependsOn 直接前驱步骤 ID。执行顺序与失效传播都以此为准。
	DependsOn []int `json:"depends_on,omitempty"`

	// Assumptions 本步骤成立所依赖的前提，自然语言，一条一句。
	// 例："知识库中存在名为《架构设计》的文档"、"用户属于租户 t_1024"。
	Assumptions []string `json:"assumptions,omitempty"`

	// Expects 本步骤预期产出的一句话描述，用于判断步骤是否"真的完成了"，
	// 而不只是"没有报错"。
	Expects string `json:"expects,omitempty"`

	Effect EffectLevel `json:"effect,omitempty"`

	// ---- 运行态，不参与序列化 ----
	Status      StepStatus `json:"-"`
	Output      string     `json:"-"`
	Attempt     int        `json:"-"`
	StaleReason string     `json:"-"`
	outcome     StepOutcome
	duration    time.Duration
	err         error
}

// Plan 任务计划。
type Plan struct {
	Goal     string     `json:"goal"`
	Steps    []PlanStep `json:"steps"`
	Revision int        `json:"-"`

	// nextID 单调递增，只增不减；模型给的 ID 一律不直接采用。
	// patch 新增步骤时由它分配全局 ID，避免模型复用旧 ID 造成冲突。
	nextID int
}

const (
	MaxPlanSteps = 8 // 单次规划生成的步骤上限：过长说明任务该拆或工具不足
	MinPlanSteps = 1

	// MaxTotalSteps 是 patch 累积后的步骤总数上限。
	// 只卡 MaxPlanSteps 会让模型通过连续 add 把计划无限加长。
	MaxTotalSteps = 12

	MaxReplanCount = 2 // v2 放开到 2，配合失效传播做一次纠错 + 一次兜底

	maxStepOutputBytes = 16 * 1024
	maxPriorBytes      = 48 * 1024
)

// Fact 一条可被后续步骤引用的结构化结论。
// Key 用命名空间避免不同步骤撞名："{step_id}.{name}"。
type Fact struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence,omitempty"`
}

// Violation 本步骤发现的前序结论不成立。
// 这是系统里唯一能让执行"回头"的信号。
type Violation struct {
	StepID   int    `json:"step_id"`
	Claim    string `json:"claim"`
	Reason   string `json:"reason"`
	Evidence string `json:"evidence,omitempty"`
}

// StepOutcome 单步执行的结构化产物。
type StepOutcome struct {
	Output     string      `json:"output"`
	Facts      []Fact      `json:"facts,omitempty"`
	Violations []Violation `json:"violations,omitempty"`
}

// ParsePlan 解析 LLM 输出的计划 JSON 并校验。
// 非法结构返回错误（调用方决定降级为普通对话，而非失败）。
func ParsePlan(raw string) (*Plan, error) {
	cleaned := strutil.ExtractJSON(raw)
	var plan Plan
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil, fmt.Errorf("计划 JSON 解析失败: %w", err)
	}
	if err := plan.NormalizeIDs(); err != nil {
		return nil, err
	}
	if err := ValidatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// NormalizeIDs 按出现顺序把步骤重排为 1..N，并同步重写 depends_on。
//
// 模型给的 ID 只用于表达"这一步依赖哪一步"，不进入系统作为全局标识。
// 让 ID 由系统规范化/分配，模型只负责语义，是避免多轮 patch 撞 ID 的最省事做法。
func (p *Plan) NormalizeIDs() error {
	if p == nil {
		return fmt.Errorf("计划为空")
	}
	if len(p.Steps) == 0 {
		return nil
	}

	oldToNew := make(map[int]int, len(p.Steps))
	for i := range p.Steps {
		old := p.Steps[i].ID
		if old > 0 {
			if _, exists := oldToNew[old]; exists {
				return fmt.Errorf("步骤 id 重复: %d", old)
			}
			oldToNew[old] = i + 1
		}
	}
	for i := range p.Steps {
		p.Steps[i].ID = i + 1
	}
	for i := range p.Steps {
		deps := p.Steps[i].DependsOn
		for j, dep := range deps {
			if mapped, ok := oldToNew[dep]; ok {
				deps[j] = mapped
			}
		}
		p.Steps[i].DependsOn = dedupeIDs(deps)
	}
	p.nextID = len(p.Steps) + 1
	return nil
}

// ValidatePlan 校验计划结构（纯函数，可单测）。
func ValidatePlan(p *Plan) error {
	return validatePlan(p, MaxPlanSteps)
}

func validatePlan(p *Plan, maxSteps int) error {
	if p == nil {
		return fmt.Errorf("计划为空")
	}
	if strings.TrimSpace(p.Goal) == "" {
		return fmt.Errorf("计划缺少目标")
	}
	if len(p.Steps) < MinPlanSteps {
		return fmt.Errorf("计划没有步骤")
	}
	if maxSteps <= 0 {
		maxSteps = MaxPlanSteps
	}
	if len(p.Steps) > maxSteps {
		return fmt.Errorf("计划步骤数 %d 超过上限 %d", len(p.Steps), maxSteps)
	}

	seen := make(map[int]bool, len(p.Steps))
	for i, s := range p.Steps {
		if s.ID <= 0 {
			return fmt.Errorf("步骤 %d 的 id 非法: %d", i+1, s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("步骤 id 重复: %d", s.ID)
		}
		seen[s.ID] = true
		if strings.TrimSpace(s.Description) == "" {
			return fmt.Errorf("步骤 %d 缺少描述", s.ID)
		}
		switch s.Effect {
		case "", EffectNone, EffectIdempotent, EffectSideEffect:
		default:
			return fmt.Errorf("步骤 %d 的 effect 非法: %q", s.ID, s.Effect)
		}
	}

	for _, s := range p.Steps {
		for _, dep := range s.DependsOn {
			if dep == s.ID {
				return fmt.Errorf("步骤 %d 依赖自身", s.ID)
			}
			if !seen[dep] {
				return fmt.Errorf("步骤 %d 依赖不存在的步骤 %d", s.ID, dep)
			}
		}
	}

	if path, ok := findCycle(p); ok {
		return fmt.Errorf("计划存在依赖环: %s", formatCyclePath(path))
	}
	return nil
}

// canTransition 是步骤状态机的唯一事实来源。
// 非法迁移返回 false，由调用方转成 error；静默忽略会让状态与日志脱节。
func canTransition(from, to StepStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case "", StatusPending:
		return to == StatusReady || to == StatusBlocked || to == StatusSkipped
	case StatusReady:
		return to == StatusRunning || to == StatusBlocked || to == StatusSkipped
	case StatusRunning:
		// 路径 B 的前提校验发生在执行前：此时步骤已经占用 running，
		// 但业务调用还没有发生。blocked 比 failed 更准确，也避免把
		// "前提不成立" 误记成执行失败后直接进入重试。
		return to == StatusDone || to == StatusFailed || to == StatusBlocked
	case StatusDone:
		return to == StatusStale
	case StatusFailed:
		return to == StatusStale || to == StatusSkipped
	case StatusBlocked:
		return to == StatusReady || to == StatusSkipped
	case StatusStale:
		return to == StatusPending || to == StatusReady || to == StatusSkipped
	case StatusSkipped:
		return false
	default:
		return false
	}
}

// Transition 执行一次受约束的状态迁移。
func (s *PlanStep) Transition(to StepStatus) error {
	from := s.Status
	if !canTransition(from, to) {
		return fmt.Errorf("步骤 %d 非法状态迁移: %s -> %s", s.ID, from, to)
	}
	s.Status = to
	return nil
}

// DeepCopy 复制计划，包含运行态。
// patch 先改副本、校验通过再整体替换，避免原地修改后回滚留下半坏状态。
func (p *Plan) DeepCopy() *Plan {
	if p == nil {
		return nil
	}
	cp := &Plan{Goal: p.Goal, Revision: p.Revision, nextID: p.nextID}
	cp.Steps = make([]PlanStep, len(p.Steps))
	for i, s := range p.Steps {
		cp.Steps[i] = s
		cp.Steps[i].DependsOn = append([]int(nil), s.DependsOn...)
		cp.Steps[i].Assumptions = append([]string(nil), s.Assumptions...)
		// outcome 是运行态，但 facts/violations 是切片；浅拷贝会让
		// patch 副本与原计划共享底层数组，审计时看到被改写的历史结果。
		cp.Steps[i].outcome.Facts = append([]Fact(nil), s.outcome.Facts...)
		cp.Steps[i].outcome.Violations = append([]Violation(nil), s.outcome.Violations...)
	}
	return cp
}

// Step 按 ID 查找步骤（返回副本指针，仅用于读取）。
func (p *Plan) Step(id int) *PlanStep {
	if p == nil {
		return nil
	}
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}

// EnsureLinearDependencies 在模型完全没有给出依赖时，退化为线性执行。
//
// 图执行的前提是模型真的声明了依赖；如果整份计划一条边都没有，
// 直接把所有步骤当成就绪会让"先写草稿再润色"变成并发乱序。
// 返回 true 表示发生了退化，调用方可以记录一条指标。
func (p *Plan) EnsureLinearDependencies() bool {
	if p == nil || len(p.Steps) < 2 {
		return false
	}
	for _, s := range p.Steps {
		if len(s.DependsOn) > 0 {
			return false
		}
	}
	for i := 1; i < len(p.Steps); i++ {
		p.Steps[i].DependsOn = []int{p.Steps[i-1].ID}
	}
	return true
}

// ReadyLayer 返回当前可执行的步骤。
// 语义：status ∈ {pending, ready, stale} 且所有 DependsOn 均 done。
// ready 也要被选中：路径 B 的 blocked 步骤在 patch 后由 ResetBlocked
// 放回 ready，如果这里只认 pending/stale，它们会永远停在 ready 上。
func (p *Plan) ReadyLayer() []*PlanStep {
	if p == nil {
		return nil
	}
	out := make([]*PlanStep, 0, len(p.Steps))
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.Status != StatusPending && s.Status != StatusReady && s.Status != StatusStale {
			continue
		}
		ready := true
		for _, dep := range s.DependsOn {
			d := p.Step(dep)
			if d == nil || d.Status != StatusDone {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, s)
		}
	}
	return out
}

// ResetBlocked 把 blocked 步骤放回 ready，供 patch 后重新调度。
// blocked 没有产出，不需要像 stale 那样走 pending 再重新校验；
// 但它的 err/outcome 是上一版计划的诊断信息，必须清掉，
// 否则续跑或综合阶段会把旧错误当成这一版的结果。
func (p *Plan) ResetBlocked() {
	if p == nil {
		return
	}
	for i := range p.Steps {
		if p.Steps[i].Status != StatusBlocked {
			continue
		}
		if err := p.Steps[i].Transition(StatusReady); err != nil {
			continue
		}
		p.Steps[i].Output = ""
		p.Steps[i].Attempt = 0
		p.Steps[i].StaleReason = ""
		p.Steps[i].outcome = StepOutcome{}
		p.Steps[i].duration = 0
		p.Steps[i].err = nil
	}
}

// AllSettled 表示没有可继续推进的步骤（done/skipped 都是终态）。
func (p *Plan) AllSettled() bool {
	if p == nil {
		return true
	}
	for _, s := range p.Steps {
		switch s.Status {
		case StatusDone, StatusSkipped:
		default:
			return false
		}
	}
	return true
}

// AllDone 表示所有步骤都成功完成（skipped 不算完成）。
func (p *Plan) AllDone() bool {
	if p == nil {
		return false
	}
	for _, s := range p.Steps {
		if s.Status != StatusDone {
			return false
		}
	}
	return true
}

// Propagate 返回所有需要作废的步骤 ID（含 seeds 本身）。
// 使用 BFS，O(V+E)；V 很小，但写成通用形式便于后续扩展。
func (p *Plan) Propagate(seeds []int) []int {
	if p == nil || len(seeds) == 0 {
		return nil
	}
	invalid := make(map[int]bool, len(seeds))
	queue := make([]int, 0, len(seeds))
	for _, id := range seeds {
		if p.Step(id) == nil || invalid[id] {
			continue
		}
		invalid[id] = true
		queue = append(queue, id)
	}

	out := make([]int, 0, len(invalid))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for i := range p.Steps {
			s := &p.Steps[i]
			if invalid[s.ID] || s.Status == StatusSkipped {
				continue
			}
			for _, dep := range s.DependsOn {
				if dep == cur {
					invalid[s.ID] = true
					queue = append(queue, s.ID)
					break
				}
			}
		}
	}
	sort.Ints(out)
	return out
}

// MarkStale 把已产出结果的步骤标记为 stale。
// pending/ready 不需要标记：它们还没跑过，没有结果要作废。
func (p *Plan) MarkStale(ids []int, reason string) {
	if p == nil {
		return
	}
	for _, id := range ids {
		s := p.Step(id)
		if s == nil {
			continue
		}
		if s.Status == StatusDone || s.Status == StatusFailed {
			if err := s.Transition(StatusStale); err == nil {
				s.StaleReason = reason
			}
		}
	}
}

// ResetStale 把 stale 步骤放回 pending，让调度器重新走 ready -> running。
func (p *Plan) ResetStale() {
	if p == nil {
		return
	}
	for i := range p.Steps {
		if p.Steps[i].Status == StatusStale {
			if err := p.Steps[i].Transition(StatusPending); err != nil {
				continue
			}
			p.Steps[i].Output = ""
			p.Steps[i].Attempt = 0
			p.Steps[i].StaleReason = ""
			p.Steps[i].outcome = StepOutcome{}
			p.Steps[i].duration = 0
			p.Steps[i].err = nil
		}
	}
}

// Results 按计划顺序导出执行结果，供综合与审计使用。
func (p *Plan) Results() []StepResult {
	if p == nil {
		return nil
	}
	out := make([]StepResult, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, StepResult{
			Step:     s,
			Output:   s.Output,
			Success:  s.Status == StatusDone,
			Stale:    s.Status == StatusStale,
			Outcome:  s.outcome,
			Attempt:  s.Attempt,
			Duration: int64(s.duration / time.Millisecond),
			Err:      s.err,
		})
	}
	return out
}

// StepResult 单步执行结果（进入后续步骤上下文与综合阶段）。
type StepResult struct {
	Step     PlanStep
	Output   string
	Success  bool
	Stale    bool
	Outcome  StepOutcome
	Attempt  int
	Duration int64
	Err      error
}

// FormatPlan 渲染计划为用户可读文本（进度展示用）。
func (p *Plan) FormatPlan() string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【任务规划】目标：%s\n", p.Goal))
	for _, s := range p.Steps {
		if s.Status == StatusSkipped {
			continue
		}
		if s.Tool != "" {
			sb.WriteString(fmt.Sprintf("%d. %s（建议工具：%s）\n", s.ID, s.Description, s.Tool))
		} else {
			sb.WriteString(fmt.Sprintf("%d. %s\n", s.ID, s.Description))
		}
	}
	return sb.String()
}

// truncateRunes 按字符（rune）截断并标注。
//
// 上限单位是「字节」还是「字符」必须一开始就定清楚：单步结果常是中文，
// 按字节切片切在多字节序列中间会产生非法 UTF-8，这段文本会作为 prompt
// 发给模型（部分供应商直接 400），也会经 onChunk 推给前端（乱码）。
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "...(截断)"
}

// truncateBytes 按字节上限截断，并保证不切断 UTF-8 序列。
func truncateBytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(截断)"
}

// FormatResults 渲染已执行步骤及其结果（构建后续 prompt 用）。
//
// 失效步骤只保留标题与"已失效"标记，正文一律不进入 prompt：
// 模型看见旧结论就很容易继续引用它，仅靠一句"不要使用"并不可靠。
func FormatResults(results []StepResult) string {
	var sb strings.Builder
	for _, r := range results {
		if r.Stale || r.Step.Status == StatusStale {
			sb.WriteString(fmt.Sprintf("步骤 %d：%s【已失效】\n该步骤结果已失效，不得作为依据。\n\n",
				r.Step.ID, r.Step.Description))
			continue
		}
		if r.Step.Status == StatusSkipped {
			continue
		}
		status := "成功"
		if !r.Success {
			status = "失败"
		}
		out := r.Output
		if !r.Success && r.Err != nil && strings.TrimSpace(out) == "" {
			out = r.Err.Error()
		}
		out = truncateBytes(out, maxStepOutputBytes)
		sb.WriteString(fmt.Sprintf("步骤 %d：%s【%s】\n结果：%s\n\n",
			r.Step.ID, r.Step.Description, status, out))
	}
	return sb.String()
}

// FormatSynthesisResults 是综合阶段专用的结果视图。
//
// 与 FormatResults 的差异：失败步骤只保留标题，不把原始错误文本送进 prompt。
// 错误信息里常带工具参数、权限提示甚至内部地址，模型很容易把它当成"事实"
// 复述给用户；综合阶段只需要知道"这一步没有可用结果"。
func FormatSynthesisResults(results []StepResult) string {
	var sb strings.Builder
	for _, r := range results {
		if r.Stale || r.Step.Status == StatusStale {
			sb.WriteString(fmt.Sprintf("步骤 %d：%s【已失效】\n该步骤结果已失效，不得作为依据。\n\n",
				r.Step.ID, r.Step.Description))
			continue
		}
		if r.Step.Status == StatusSkipped {
			continue
		}
		if !r.Success {
			sb.WriteString(fmt.Sprintf("步骤 %d：%s【失败/未完成】\n该步骤没有产出可用结果，不得猜测其内容。\n\n",
				r.Step.ID, r.Step.Description))
			continue
		}
		out := truncateBytes(r.Output, maxStepOutputBytes)
		sb.WriteString(fmt.Sprintf("步骤 %d：%s【成功】\n结果：%s\n\n",
			r.Step.ID, r.Step.Description, out))
	}
	return sb.String()
}

// FormatPrior 把前序结果压进单步 prompt。
//
// 单条 16KB × 7 步会挤掉工具结果与回答空间，所以对 prior 再做一次总量约束；
// 超出时按「离当前步骤由近到远」保留，越早的结果越可能已经被后续步骤引用过。
func FormatPrior(results []StepResult) string {
	var parts []string
	total := 0
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		if !r.Success || r.Stale || r.Step.Status == StatusSkipped {
			continue
		}
		text := fmt.Sprintf("步骤%d【%s】结果：%s\n\n",
			r.Step.ID, r.Step.Description, truncateBytes(r.Output, maxStepOutputBytes))
		if total+len(text) > maxPriorBytes {
			break
		}
		parts = append(parts, text)
		total += len(text)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "")
}

// ParseStepOutcome 从单步输出末尾解析结构化 JSON。
// 解析失败不报错，退化为"只有正文、无 facts/violations"——
// 结构化尾块是增强信息，不能因为模型漏写就让整轮执行失败。
func ParseStepOutcome(output string) StepOutcome {
	outcome := StepOutcome{Output: output}
	idx := strings.LastIndex(output, "{")
	for idx >= 0 {
		candidate := strutil.ExtractJSON(output[idx:])
		var parsed StepOutcome
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil &&
			(parsed.Facts != nil || parsed.Violations != nil) {
			if parsed.Output == "" {
				parsed.Output = strings.TrimSpace(output[:idx])
			}
			return parsed
		}
		idx = strings.LastIndex(output[:idx], "{")
	}
	return outcome
}

func dedupeIDs(ids []int) []int {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// findCycle 用三色 DFS 找环，并返回环上的节点顺序。
func findCycle(p *Plan) ([]int, bool) {
	adj := make(map[int][]int, len(p.Steps))
	for _, s := range p.Steps {
		adj[s.ID] = append([]int(nil), s.DependsOn...)
	}
	color := make(map[int]int, len(p.Steps)) // 0 未访问，1 栈中，2 完成
	stack := make([]int, 0, len(p.Steps))
	var cycle []int

	var dfs func(int) bool
	dfs = func(id int) bool {
		color[id] = 1
		stack = append(stack, id)
		for _, next := range adj[id] {
			switch color[next] {
			case 1:
				start := 0
				for i, v := range stack {
					if v == next {
						start = i
						break
					}
				}
				cycle = append(cycle, stack[start:]...)
				cycle = append(cycle, next)
				return true
			case 0:
				if dfs(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = 2
		return false
	}

	ids := make([]int, 0, len(p.Steps))
	for id := range adj {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if color[id] == 0 && dfs(id) {
			return cycle, true
		}
	}
	return nil, false
}

func formatCyclePath(path []int) string {
	parts := make([]string, len(path))
	for i, id := range path {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, " -> ")
}
