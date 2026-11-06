package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ResumeWindow 续跑窗口。
//
// 超过 10 分钟的运行不再自动续跑：外部状态（文档、权限、消息）可能已经变化，
// 复用旧步骤结果比重新执行更危险。
const ResumeWindow = 10 * time.Minute

// RunRecord 一次规划执行的快照。
//
// planner 包只定义数据结构与读写接口，不 import gorm；
// 具体落库由 service 层的 DAO 实现，便于单测用内存实现替换。
type RunRecord struct {
	ID             string
	BotID          string
	ConversationID string
	UserID         string
	Goal           string
	GoalHash       string
	Status         RunStatus
	Revision       int
	Plan           *Plan
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// StepRecord 单步执行记录，同时承担审计与副作用重放。
type StepRecord struct {
	ID         string
	RunID      string
	StepID     int
	Revision   int
	Attempt    int
	Status     StepStatus
	Effect     EffectLevel
	InputHash  string
	Output     string
	Facts      []Fact
	Violations []Violation
	TokensIn   int
	TokensOut  int
	DurationMs int64
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// RunPatch 运行记录的增量更新。
// 指针字段用于区分"没传"和"显式清空"。
type RunPatch struct {
	Status   *RunStatus
	Revision *int
	Plan     *Plan
	Error    *string
}

// RunMeta 标识运行归属；ResumeRunID 非空时尝试续跑。
type RunMeta struct {
	BotID          string
	ConversationID string
	UserID         string
	ResumeRunID    string
}

// RunStore 运行记录存储接口。
//
// GetRun / ListSteps 是续跑必需的读路径，规格里的最小接口没有列全；
// 没有它们，"从 checkpoint 续跑"只能靠扫全表或猜状态。
type RunStore interface {
	CreateRun(ctx context.Context, run *RunRecord) error
	UpdateRun(ctx context.Context, runID string, patch RunPatch) error
	AppendStep(ctx context.Context, rec *StepRecord) error
	FindStepOutput(ctx context.Context, runID string, stepID int, inputHash string) (*StepRecord, error)
	GetRun(ctx context.Context, runID string) (*RunRecord, error)
	LatestRun(ctx context.Context, conversationID string) (*RunRecord, error)
	ListSteps(ctx context.Context, runID string) ([]*StepRecord, error)
}

// StepUpdater 是可选的 checkpoint 原地更新能力。
//
// 不把它塞进 RunStore 是刻意的：只读的运行仓库、测试替身仍然可以只实现
// AppendStep；支持更新的实现则能把一步的 running/final 合并成一行，
// 既减少写放大，也让续跑读取到的就是最终状态。
type StepUpdater interface {
	UpdateStep(ctx context.Context, rec *StepRecord) error
}

// GoalHash 归一化目标后取哈希，用于判断"同一个问题"能否续跑。
func GoalHash(goal string) string {
	normalized := strings.Join(strings.Fields(goal), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// StepInputHash 计算步骤输入指纹，用于副作用重放去重。
//
// 只包含逻辑输入（步骤定义 + 前序结果），不包含 attempt：
// 重试必须命中同一条记录，否则"重试"会变成"再执行一次副作用"。
//
// assumptions / expects 也必须进入指纹：patch 允许只改这两项而不改描述，
// 如果指纹不变，一个已经执行过的副作用步骤会拿旧前提下的结果直接重放。
// 这两段按"有值才追加"的方式拼接，保证未声明前提/预期的老记录哈希不变。
func StepInputHash(step PlanStep, prior string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\n%s\n%s\n%s\n", step.ID, step.Tool, step.Description, prior)
	for _, assumption := range step.Assumptions {
		fmt.Fprintf(h, "a:%s\n", assumption)
	}
	if strings.TrimSpace(step.Expects) != "" {
		fmt.Fprintf(h, "e:%s\n", step.Expects)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PlanStats 返回计划规模，供日志与指标使用。
// maxDepth 按依赖边做拓扑深度；有环时由 ValidatePlan 提前拦截，这里不再重复报错。
func PlanStats(plan *Plan) (steps, edges, maxDepth int) {
	if plan == nil {
		return 0, 0, 0
	}
	steps = len(plan.Steps)
	depth := make(map[int]int, steps)
	for _, s := range plan.Steps {
		edges += len(s.DependsOn)
	}
	var visit func(id int) int
	visit = func(id int) int {
		if d, ok := depth[id]; ok {
			return d
		}
		step := plan.Step(id)
		if step == nil {
			return 0
		}
		max := 0
		for _, dep := range step.DependsOn {
			if d := visit(dep); d > max {
				max = d
			}
		}
		depth[id] = max + 1
		if depth[id] > maxDepth {
			maxDepth = depth[id]
		}
		return depth[id]
	}
	for _, s := range plan.Steps {
		visit(s.ID)
	}
	return steps, edges, maxDepth
}

// hydrateResume 把运行快照与步骤记录合并回可执行计划。
//
// 关键规则：
//   - done 且非 side_effect：直接复用，不再调用模型/工具；
//   - done 但 side_effect：延迟到执行前按 input_hash 复核，防止重复副作用；
//   - failed：回到 pending 重试（失败记录本身保留供审计）；
//   - running：进程可能死在半途，标记 stale，走完整 pending -> ready 迁移。
func hydrateResume(run *RunRecord, steps []*StepRecord) (*Plan, error) {
	if run == nil || run.Plan == nil {
		return nil, fmt.Errorf("运行记录缺少计划快照")
	}
	plan := run.Plan.DeepCopy()
	plan.Revision = run.Revision
	maxID := 0
	for i := range plan.Steps {
		plan.Steps[i].Status = StatusPending
		plan.Steps[i].Output = ""
		plan.Steps[i].Attempt = 0
		plan.Steps[i].outcome = StepOutcome{}
		plan.Steps[i].duration = 0
		plan.Steps[i].err = nil
		if plan.Steps[i].ID > maxID {
			maxID = plan.Steps[i].ID
		}
	}
	plan.nextID = maxID + 1

	latest := latestStepRecords(steps)
	for _, rec := range latest {
		step := plan.Step(rec.StepID)
		if step == nil {
			continue
		}
		switch rec.Status {
		case StatusDone:
			step.Attempt = rec.Attempt
			step.duration = time.Duration(rec.DurationMs) * time.Millisecond
			if step.Effect == EffectSideEffect {
				// 交给 runStep 用当前 input_hash 复核；此处不能直接复用。
				step.Status = StatusPending
				continue
			}
			step.Status = StatusDone
			step.Output = rec.Output
			step.outcome = StepOutcome{
				Output:     rec.Output,
				Facts:      append([]Fact(nil), rec.Facts...),
				Violations: append([]Violation(nil), rec.Violations...),
			}
		case StatusRunning, StatusStale:
			step.Status = StatusStale
			step.StaleReason = "上次运行中断，需要重跑"
		case StatusFailed:
			step.Status = StatusPending
		}
	}
	return plan, nil
}

// latestStepRecords 按 (step_id) 取 revision/attempt 最大的一条。
func latestStepRecords(records []*StepRecord) map[int]*StepRecord {
	out := make(map[int]*StepRecord, len(records))
	for _, rec := range records {
		if rec == nil {
			continue
		}
		cur, ok := out[rec.StepID]
		if !ok || rec.Revision > cur.Revision ||
			(rec.Revision == cur.Revision && rec.Attempt > cur.Attempt) {
			out[rec.StepID] = rec
		}
	}
	return out
}

// sortedFacts 把结果里的 facts 压成校验 prompt 用的摘要（按 key 排序）。
func sortedFacts(results []StepResult, limit int) string {
	type kv struct{ key, value string }
	var items []kv
	for _, r := range results {
		if r.Stale || !r.Success {
			continue
		}
		for _, f := range r.Outcome.Facts {
			items = append(items, kv{key: f.Key, value: f.Value})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	var sb strings.Builder
	for _, item := range items {
		line := fmt.Sprintf("%s = %s\n", item.key, item.value)
		if limit > 0 && sb.Len()+len(line) > limit {
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
