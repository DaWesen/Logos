# Planner v2 规格：从线性计划到可失效传播的执行图

> 交接对象：负责实现的下游 AI / 工程师
> 代码基线：`Logos` 仓库当前 `main` 分支
> 目标包：`internal/bot/planner`
> 要求：按 P0 → P3 分阶段落地，每阶段可独立合入、独立回滚、独立验证。

---

## 0. 交接须知（先读这一节）

1. 这是一个**已有系统的增量改造**，不是重写。现有 13 个测试必须继续通过（见 §15.1）。
2. 不要引入新的 Agent 框架、工作流引擎或图计算库。8 个节点不值得上 `gonum`，手写 Kahn 拓扑排序即可。
3. 不要修改消息链路、网关、Outbox、Kafka 相关代码。本规格的作用域**只有** `internal/bot/planner`、`internal/service/ai/bot` 的接线部分，以及新增的持久化模型。
4. 每阶段结束必须能回答："如果现在回滚，用户会看到什么变化？" 答不上来说明改动没有隔离好。
5. 代码注释用中文，解释 **why** 而不是 **what**，与现有风格保持一致。
6. 文中所有数字都是**设计目标**，不是实测值。任何写进代码注释或简历的数字，必须能用 §15 的测试或 §14 的指标复现。

---

## 1. 现状

### 1.1 当前实现

| 能力 | 位置 | 说明 |
|---|---|---|
| 计划生成 | `internal/bot/planner/planner.go:43` | 一次 LLM 调用，输出 JSON |
| 计划解析与校验 | `internal/bot/planner/plan.go:61` | 只校验结构：非空、1–8 步、ID 正且唯一、描述非空 |
| 顺序执行 | `internal/bot/planner/planner.go:121` | 线性数组，逐条执行，首个失败即停 |
| 重规划 | `internal/bot/planner/planner.go:53` | 输入失败原因，输出"剩余步骤"，ID 从 1 重排 |
| 重规划次数 | `internal/bot/planner/plan.go:55` | 全局计数，最多 1 次 |
| 综合作答 | `internal/bot/planner/planner.go:67` | 把全部 `StepResult` 拼进 prompt |
| 降级 | `internal/service/ai/bot/service/bot.go:949`、`:1166` | 任一环节 error → 回退普通对话 |

### 1.2 执行流

```
CreatePlan → ParsePlan → ValidatePlan
   → runSteps（顺序、首个失败即停、prior 累积成功输出）
   → 有失败？ → Replan（只输出剩余步骤）→ 替换整个 plan → 重跑
   → 无失败或 replan 用尽 → Synthesize
   → 全失败 / 任何 error → 调用方降级普通对话
```

### 1.3 关键约束

```go
MaxPlanSteps    = 8   // plan.go:53
MinPlanSteps    = 1
MaxReplanCount  = 1   // plan.go:55
maxStepOutputKB = 16  // plan.go:56
```

---

## 2. 缺陷清单

按严重度排序。D1–D4 是**能力缺失**，D5–D9 是**实现缺陷**，D10–D11 是**可观测性缺失**。

### D1 没有"前提"这个概念

`PlanStep` 只有 `ID / Description / Tool`。模型在第 2 步做出的隐含假设（"文件是 CSV"、"用户属于该租户"、"接口支持分页"）从未被记录，因此：

- 第 7 步无法"作废第 2 步"，因为没有可寻址的对象；
- 重规划无法知道"哪些结论还成立"，只能全量重来或直接放弃。

**这是本规格要解决的核心问题。**

### D2 失效触发条件只有 error

`runSteps` 只在 `exec` 返回非 nil error 时停止。而"第 7 步发现第 2 步错了"通常不是工具报错，是模型在自然语言里陈述矛盾——这种结果 `Success == true`，系统认为它成功了。

**结论：现有实现在语义上无法表达"逻辑失败"，只能表达"执行失败"。**

### D3 重规划的契约禁止回退

`replanSystemPrompt` 明确要求"只输出尚未完成的剩余步骤（重新从 id 1 计数）"。模型没有语法能力说"回到第 2 步重做"。即使模型想说，输出格式也不允许。

### D4 线性结构没有依赖边

`Steps []PlanStep` 是数组。系统不知道第 7 步依赖第 2 步，因此即使检测到失效，也圈不出影响范围。

### D5 `replans` 是全局计数器

`planner.go:86` 的 `replans` 在整个 `ExecutePlan` 生命周期内共享。第 2 步失败消耗掉唯一一次配额后，第 6 步再失败直接终止。如果产品语义是"每步允许重试一次"，这里是 bug；如果是"整轮只允许一次重规划"，注释必须写清楚，否则会被评审追问。

### D6 重规划后 `Goal` 可能漂移

`Replan` 返回的 `*Plan` 整体替换旧 plan（`planner.go:113`），而 `newPlan.Goal` 来自模型输出。模型可能改写目标，导致后续轮次朝错误方向执行。**原始 goal 必须是不可变输入，不能被模型覆盖。**

### D7 重规划的输入只有"本轮"结果

`planner.go:108` 传的是 `results`（本轮 `runSteps` 的返回值），不是 `allResults`。当前 `MaxReplanCount = 1` 所以无影响；一旦放开次数，第二次重规划会丢失第一轮的历史。

### D8 `maxStepOutputKB` 单位错配，16KB 从未生效

```go
// plan.go:147
out := truncateRunes(r.Output, maxStepOutputKB*1024)
```

`truncateRunes` 第二参是 **rune 数**，传入 16384。中文单字 3 字节，实际截到约 **48KB** 才停。`planner.go:180` 的 `truncateForContext` 是同一个错误。

讽刺的是 `truncateRunes` 上方的注释正在讨论"上限单位是字节还是字符必须一开始就定清楚"。

### D9 `prior` 无预算累积

`runSteps` 中 `prior` 每步追加一次 `truncateForContext(output)`，到第 8 步时前面 7 步的输出全部进入 prompt。按 D8 的实际截断值估算，最坏 7 × 48KB ≈ 336KB 字符。没有任何 token 预算、没有分层摘要，只有单条截断。

### D10 失败输出污染综合

失败步骤的 `Output` 存的是错误字符串（如"工具连接超时"），但 `Synthesize` 拿到全部 `StepResult`，模型可能把基础设施报错当成事实写进最终答案。

### D11 无持久化、无观测

- 进程重启 / 请求超时后，整轮执行结果全部丢失，无法续跑、无法审计；
- `StepResult` 不记录耗时、token、attempt，无法定位"慢在哪一步"；
- 没有 metrics，无法回答"重规划率多少"、"平均几步"、"降级率多少"。

---

## 3. 目标与非目标

### 3.1 目标

| 编号 | 目标 | 可验证信号 |
|---|---|---|
| G1 | 步骤可声明前提与依赖 | `PlanStep.Assumptions`、`DependsOn` 被解析并校验 |
| G2 | 步骤能上报"前序结论被推翻" | `StepOutcome.Violations` 非空时触发失效传播 |
| G3 | 失效沿依赖图传播 | 被推翻步骤的全部下游标记 `stale` |
| G4 | 重规划可回退、可局部替换 | patch 协议支持 `add / modify / remove / replace_from` |
| G5 | 未受影响的步骤结果被复用 | 失效步骤数 < 总步骤数时，复用计数 > 0 |
| G6 | 失败分级 | 瞬时失败重试不消耗重规划配额 |
| G7 | 预算硬约束 | 任意路径下 LLM 调用数 ≤ `MaxLLMCalls`，超限必然降级 |
| G8 | 可恢复 | 同一 `run_id` 可从 checkpoint 续跑，已完成步骤不重复调用 |
| G9 | 副作用可重放 | `Effect != none` 的步骤重放时命中执行记录，不产生第二次副作用 |
| G10 | 可观测 | 7 个核心 metric + 结构化日志字段（§14） |

### 3.2 非目标

- **不做**通用 workflow engine。步骤之间不传递自定义数据结构，只通过 `Facts` 与文本结果通信。
- **不做**跨请求的计划缓存。相同 goal 不保证复用计划。
- **不做**人在回路（human-in-the-loop）审批。留出接口，不实现。
- **不保证**语义正确性。系统只保证"前提被显式声明、矛盾被显式上报、失效被显式传播"，不保证模型上报的矛盾一定正确。
- **不改**单步内部的 ReAct 循环。每步仍由 `BotAgent.Chat` 执行，工具调用逻辑不变。

---

## 4. 设计总览

```
                         ┌──────────────┐
                         │  CreatePlan  │  LLM #1，要求输出 depends_on/assumptions
                         └──────┬───────┘
                                ▼
                    ┌───────────────────────┐
                    │ ValidatePlan (DAG)    │  环检测、引用完整性、effect 合法性
                    └──────────┬────────────┘
                               ▼
        ┌──────────────────────────────────────────┐
        │  Scheduler：拓扑分层，层内并发 ≤ 4        │
        └───────┬───────────────────────┬──────────┘
                │ ready steps           │ progress events（串行化后回调）
                ▼                       ▼
        ┌──────────────┐        ┌─────────────┐
        │ StepExecutor │        │  onProgress │
        │ ReAct + 工具 │        └─────────────┘
        └──────┬───────┘
               ▼
      ┌──────────────────┐
      │   StepOutcome    │  Output + Facts + Violations
      └────────┬─────────┘
               ▼
   ┌────────────────────────────┐   无失效
   │ StalePropagate + Decide    │──────────────► Synthesize（带 stale 声明）
   └───────────┬────────────────┘
               │ 有失败 / 有矛盾
               ▼
        ┌──────────────┐  预算充足
        │ ReplanPatch  │──────────► apply → revision++ → 回到 Scheduler
        └──────────────┘  预算耗尽 ──► Synthesize(partial) 或 降级
```

三条不变式：

1. **Goal 不可变**。只有用户输入能改 goal，模型只能改步骤。
2. **结果只增不改**。`(run_id, step_id, revision, attempt)` 一旦落库不再修改，新结果追加新行。
3. **失效单向传播**。`stale` 只能由上游传播给下游，不能反向。下游失败不会让上游失效。

---

## 5. 数据模型

### 5.1 步骤

```go
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

type PlanStep struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
	Tool        string `json:"tool,omitempty"`

	// DependsOn 直接前驱步骤 ID。执行顺序与失效传播都以此为准。
	DependsOn []int `json:"depends_on,omitempty"`

	// Assumptions 本步骤成立所依赖的前提，自然语言，一条一句。
	// 例："知识库中存在名为《架构设计》的文档"、"用户属于租户 t_1024"。
	// 执行前用于校验，执行后若被证伪则上报 Violation。
	Assumptions []string `json:"assumptions,omitempty"`

	// Expects 本步骤预期产出的一句话描述，用于判断步骤是否"真的完成了"，
	// 而不只是"没有报错"。例："得到一个包含日期、参与人、决议的列表"。
	Expects string `json:"expects,omitempty"`

	Effect EffectLevel `json:"effect,omitempty"`

	// ---- 运行态，不参与序列化 ----
	Status      StepStatus `json:"-"`
	Output      string     `json:"-"`
	Attempt     int        `json:"-"`
	StaleReason string     `json:"-"`
}
```

### 5.2 状态机

```go
const (
	StatusPending StepStatus = "pending" // 初始
	StatusReady   StepStatus = "ready"   // 依赖全部 done，等待调度
	StatusRunning StepStatus = "running"
	StatusDone    StepStatus = "done"
	StatusFailed  StepStatus = "failed"  // 永久失败，等待重规划
	StatusBlocked StepStatus = "blocked" // 依赖 failed / stale，本轮不可执行
	StatusStale   StepStatus = "stale"   // 上游失效，结果作废，需重跑
	StatusSkipped StepStatus = "skipped" // 被 patch 移除，保留记录用于审计
)
```

合法迁移（实现时用一张表约束，非法迁移返回 error 而不是静默忽略）：

| From | To |
|---|---|
| pending | ready, blocked, skipped |
| ready | running, blocked, skipped |
| running | done, failed |
| done | stale |
| failed | stale, skipped |
| blocked | ready, skipped |
| stale | pending, ready, skipped |

**注意**：`done → stale → pending → ready` 是核心回路。任何实现如果让 `stale` 直接变成 `running`，会跳过重新校验。

运行级状态是**另一套枚举**，不要复用 `StepStatus`（两者语义不同，混用会让日志无法区分"这一步失败了"和"整轮失败了"）：

```go
const (
	RunRunning         = "running"
	RunCompleted       = "completed"
	RunDegraded        = "degraded"        // 有步骤未完成，但产出了部分答案
	RunBudgetExhausted = "budget_exhausted"
	RunBlocked         = "blocked"         // 依赖图卡死（正常不该出现，出现即为 bug）
	RunFailed          = "failed"
)
```

### 5.3 执行产物

```go
// Fact 一条可被后续步骤引用的结构化结论。
// Key 用命名空间避免不同步骤撞名："{step_id}.{name}"。
type Fact struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"` // 0–1，模型自评，仅用于排序不用于判断
	Evidence   string  `json:"evidence,omitempty"`
}

// Violation 本步骤发现的前序结论不成立。
// 这是系统里唯一能让执行"回头"的信号。
type Violation struct {
	StepID   int    `json:"step_id"` // 被推翻的步骤
	Claim    string `json:"claim"`   // 被推翻的具体结论
	Reason   string `json:"reason"`
	Evidence string `json:"evidence,omitempty"`
}

type StepOutcome struct {
	Output     string      `json:"output"`
	Facts      []Fact      `json:"facts,omitempty"`
	Violations []Violation `json:"violations,omitempty"`
}
```

### 5.4 计划

```go
type Plan struct {
	Goal     string     `json:"goal"`
	Steps    []PlanStep `json:"steps"`
	Revision int        `json:"-"`
	nextID   int        `json:"-"` // 单调递增，只增不减；模型给的 ID 一律不直接采用
}
```

**ID 分配规则（重要）**：

- `CreatePlan` 时接受模型给的 ID（1..N），但立刻做一次规范化：按出现顺序重排为 1..N。
- 之后所有 patch 新增的步骤，ID 由 `nextID++` 分配，**不采用模型给的 ID**。
- 模型在 patch 里只能使用 `local_id`（本次 patch 内临时标识），由应用层映射为全局 ID 并重写 `depends_on`。

理由：模型在多轮 patch 中几乎必然复用旧 ID 或给出冲突 ID，靠校验去挡会频繁失败；让 ID 由系统单调分配，模型只负责语义，是最省事的正确做法。

---

## 6. 执行算法

### 6.1 主循环

```go
func (p *Planner) Execute(ctx context.Context, plan *Plan, exec StepExecutor) (*RunResult, error) {
	run := newRun(plan) // 分配 run_id、初始化预算、写 checkpoint
	defer run.Close()

	for {
		if err := run.Budget.Check(); err != nil {
			return run.Finish(RunBudgetExhausted), err
		}

		// 1. 选层：所有依赖已 done 且自身 pending/stale 的步骤
		layer := plan.ReadyLayer()
		if len(layer) == 0 {
			// 没有可执行步骤：要么全完成，要么被 blocked 卡死
			if plan.AllSettled() {
				break
			}
			return run.Finish(RunBlocked), ErrDeadlock
		}

		// 2. 执行（层内并发，副作用步骤串行）
		results := p.scheduler.Run(ctx, layer, exec, run)

		// 3. 归并结果
		seeds := run.CollectInvalidations(results) // failed 步骤 + violations 目标
		if len(seeds) == 0 && run.AllDone() {
			break
		}

		// 4. 传播失效
		stale := plan.Propagate(seeds)
		if len(stale) > 0 {
			plan.MarkStale(stale, run.Reasons(seeds))
		}

		// 5. 重规划决策
		if !run.CanReplan() {
			break // 次数或预算用尽，走部分综合
		}
		patch, err := p.Replan(ctx, run.Snapshot(), stale, run.Reasons(seeds))
		if err != nil {
			run.RecordReplanError(err)
			break
		}
		if err := plan.ApplyPatch(patch); err != nil {
			run.RecordReplanError(err)
			break
		}
		plan.Revision++
		run.Checkpoint(plan)
	}
	return run.Finish(RunCompleted), nil
}
```

### 6.2 调度

```go
// ReadyLayer 返回当前可执行的步骤。
// 语义：status ∈ {pending, stale} 且所有 DependsOn 均 done。
func (p *Plan) ReadyLayer() []*PlanStep
```

层内并发规则：

- 并发上限 `min(cfg.MaxConcurrency, 4)`，默认 4。
- **`EffectSideEffect` 的步骤在该层内串行执行**，且排在只读步骤之后。理由：副作用步骤之间可能存在隐含顺序（先建目录再上传），模型没有声明依赖时，串行是最保守的正确选择。
- `EffectIdempotent` 可并行，但必须携带幂等键。
- 并发执行前先 `Budget.TryReserve(n)`，预留不到则降级为串行，避免并发打爆配额。

### 6.3 进度事件必须串行化

现有 `onProgress func(string)` 是纯文本回调。并行执行后会有多个 goroutine 同时调用，前端会看到交错乱序的文本。

改造：

```go
type ProgressEvent struct {
	Type    string // plan / step_start / step_done / step_failed / stale / replan / done
	StepID  int
	Attempt int
	Message string
	At      time.Time
}

type ProgressListener func(ProgressEvent)
```

- 事件通过带缓冲 channel 汇聚到单一 goroutine 再回调，保证顺序。
- channel 满时**丢弃 `step_start` 类事件，保留 `step_done / stale / replan`**。进度可以丢，状态不能丢。
- 兼容旧签名：保留 `func(string)` 适配器，把 `ProgressEvent` 拼成文本。

---

## 7. 失效检测与传播

### 7.1 三条检测路径（按成本从低到高）

**路径 A：执行器自报（主路径，零额外成本）**

在单步输出的末尾追加结构化尾块，由 `BuildStepPrompt` 注入协议说明：

```
【结构化输出】
完成上述步骤后，在回答末尾追加一行 JSON（不要用代码块包裹）：
{"facts":[{"key":"2.doc_ids","value":"d_1,d_2"}],
 "violations":[{"step_id":2,"claim":"文件是 CSV","reason":"实际是 XLSX，含合并单元格"}]}
没有可上报的内容时输出 {"facts":[],"violations":[]}。
如果发现前序步骤的结论不成立，**必须**写进 violations，不要只在正文里提及。
```

解析失败不报错，退化为"无 facts / 无 violations"。解析用 `strutil.ExtractJSON`（已存在），取最后一个能解析的 JSON 对象。

**路径 B：前置校验（仅当步骤声明了 assumptions）**

执行前，若 `len(step.Assumptions) > 0`，用一次廉价调用判定前提是否成立：

```
已知事实：{facts 摘要，按 key 排序，总长 ≤ 2000 字符}
待校验前提：
1. ...
2. ...
只输出 JSON：{"holds": true|false, "violated": [{"index":1,"reason":"..."}]}
```

- `max_tokens` 设 200，`temperature = 0`。
- 结果按 `hash(assumptions + facts_version)` 缓存，同一轮内不重复校验。
- 前提不成立 → 直接把该步骤标 `blocked`，并把 `violated` 转成 `Violation` 指向对应上游。

**路径 C：后置断言（兜底，默认关闭）**

对 `Expects` 非空且执行成功的步骤，用一次调用判定"产出是否满足 Expects"。默认关闭，配置 `planner.verify_expects = true` 才启用。理由：这是纯成本，且模型自评"我完成了吗"的准确率并不高。

### 7.2 传播算法

```go
// Propagate 返回所有需要作废的步骤 ID（含 seeds 本身）。
// 使用 BFS，O(V+E)；V ≤ 8，但写成通用形式便于后续扩展。
func (p *Plan) Propagate(seeds []int) []int {
	invalid := make(map[int]bool, len(seeds))
	queue := make([]int, 0, len(seeds))
	for _, id := range seeds {
		if !invalid[id] {
			invalid[id] = true
			queue = append(queue, id)
		}
	}

	out := make([]int, 0, len(seeds))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for _, s := range p.Steps {
			if invalid[s.ID] {
				continue
			}
			if contains(s.DependsOn, cur) {
				invalid[s.ID] = true
				queue = append(queue, s.ID)
			}
		}
	}
	return out
}
```

补充规则：

- 只有 `Status ∈ {done, failed}` 的步骤会被实际标记为 `stale`；`pending` 的直接保持 `pending`（它还没跑过，没有结果要作废）。
- `skipped` 不参与传播，也不复活。
- 传播必须**幂等**：对同一 seeds 调用两次，结果一致，不产生重复标记。

### 7.3 一个必须写进测试的边界

如果 violation 指向的 `step_id` 不存在（模型幻觉），**忽略该条 violation 并记一条 warn 日志**，不要让整个计划失败。理由是：模型编造 step_id 的概率远高于它编造一个真实矛盾的代价，为了一个格式错误放弃整轮执行不划算。

---

## 8. 重规划协议 v2

### 8.1 输出格式

不再输出"新计划"，输出**对现有计划的补丁**：

```json
{
  "reasoning": "第 2 步假设文件为 CSV，实际为 XLSX，需重新解析并重跑依赖它的步骤",
  "operations": [
    {"op": "replace_from", "id": 2, "steps": [
      {"local_id": "a", "description": "用 XLSX 解析器重新读取文件", "depends_on": [], "effect": "none"},
      {"local_id": "b", "description": "提取合并单元格中的表头", "depends_on": ["a"], "effect": "none"}
    ]},
    {"op": "modify", "id": 5, "patch": {"assumptions": ["表头已正确展开"]}}
  ]
}
```

### 8.2 操作语义

| op | 语义 | 约束 |
|---|---|---|
| `add` | 追加新步骤 | `local_id` 必填且本次 patch 内唯一；`depends_on` 可引用全局 ID 或 local_id |
| `modify` | 修改既有步骤 | 只允许改 `description / tool / assumptions / expects / depends_on`；**不允许改 `effect`**（降级副作用等级必须走单独的审计接口） |
| `remove` | 移除步骤 | 标 `skipped`，不物理删除；若其下游仍依赖它，下游一并 skipped |
| `replace_from` | 从某步起整段替换 | 该步及其全部下游标 `skipped`，插入新链；新链第一个步骤的 `depends_on` 继承被替换步骤的 `depends_on` |

**不允许** `op: "reset"` 或"清空重来"。那等于放弃增量，退化成 v1。

### 8.3 应用与校验

```go
func (p *Plan) ApplyPatch(patch *PlanPatch) error
```

步骤：

1. 校验每个 op 的 `id` 存在（`add` 除外）。
2. 分配全局 ID：`local_id → nextID++`，建立映射表。
3. 重写所有 `depends_on`：先查全局 ID，查不到再查 local 映射，都查不到则报错。
4. 应用变更。
5. 跑一次完整的 `ValidatePlan`（含环检测）。**校验失败则整个 patch 回滚**，plan 保持原样，计一次 replan 失败。
6. 返回受影响步骤集合，用于日志与指标。

回滚的实现方式：**不要**"先改再改回来"。先 `clone := plan.DeepCopy()`，在 clone 上应用并校验，通过后 `*plan = *clone`。原地打补丁再回滚，在 `Steps` 切片被多处引用时会出现"回滚了一半"的脏状态，这是这类代码最常见的 bug。

`ValidatePlan` 的上限要区分两个数：单次 plan 生成 ≤ 8 步（`MaxPlanSteps`），patch 累积后总数 ≤ 12 步（`MaxTotalSteps`）。只卡前者会让模型通过连续 `add` 把计划无限加长。

### 8.4 重规划 prompt 必须携带的信息

```
原始目标（不可修改）：{goal}
当前计划（revision {n}）：
  [1] done    检索知识库 …  结果摘要：…
  [2] done    解析文件 …    结果摘要：…
  [3] stale   汇总数据 …    失效原因：上游 [2] 的结论被推翻
  [4] pending 生成报告 …
已知事实：
  2.doc_ids = d_1,d_2
  2.format = XLSX
失效集合：[2,3]
失效原因：
  - 步骤 7 报告：步骤 2 声称"文件是 CSV"，实际为 XLSX
可用预算：剩余 {k} 次模型调用、{t} 秒
```

**必须显式给出预算**。模型不知道预算时会倾向于生成更长的计划，这是实测中很常见的失败模式。

---

## 9. 预算与降级

### 9.1 预算结构

```go
type Budget struct {
	MaxLLMCalls  int           // 默认 80
	MaxTokens    int           // 默认 150_000
	MaxWallClock time.Duration // 默认 180s
	MaxReplans   int           // 默认 2（v2 放开到 2，配合 stale 传播）
	MaxSteps     int           // 默认 12（含 patch 新增）
}
```

并发安全：内部用 `atomic.Int64` + `CompareAndSwap` 扣减，`TryReserve` 返回是否成功，不做阻塞等待。

### 9.2 上限推导（写进注释，不要拍脑袋）

```
单轮最坏调用数
  = 1（规划）
  + Σ_steps (1 + MaxRetries) × AvgReActCalls
  + MaxReplans × (1 + AvgReplannedSteps × AvgReActCalls)
  + 1（综合）

取 MaxSteps=8, MaxRetries=1, AvgReActCalls=3, MaxReplans=2, AvgReplannedSteps=3：
  = 1 + 8×2×3 + 2×(1+3×3) + 1
  = 1 + 48 + 20 + 1
  = 70

留 15% 余量 → MaxLLMCalls = 80
```

`AvgReActCalls` 必须实测后回填。在拿到真实分布之前，注释里写"按 3 估算，待 §14 指标校准"。

### 9.3 降级顺序（必须按此顺序，不可跳步）

1. 并发降到 1；
2. 关闭路径 C（后置断言）；
3. `MaxReplans` 减到 0，直接综合已有结果；
4. 返回部分结果 + 明确告知用户"任务未全部完成"；
5. 抛错，由调用方降级普通对话。

**降级不是失败**。第 4 步必须产出一段可读回答，而不是空字符串。空字符串会被上层当成成功并计费（参见 `internal/bot/agent/agent.go` 中 `ErrEmptyResponse` 的注释，这是同一个坑）。

### 9.4 单步输出预算（修 D8 + D9）

```go
const (
	// maxStepOutputBytes 单步结果进入后续上下文的字节上限。
	// 用字节而不是 rune：中文 3 字节/字，按 rune 计会让实际体积膨胀 3 倍，
	// 而真正约束上下文的是字节数（供应商按 token 计费，token ≈ 字节/3~4）。
	maxStepOutputBytes = 16 * 1024

	// maxPriorBytes 所有前序结果进入单步 prompt 的总字节上限。
	// 单条 16KB × 7 步 = 112KB 会挤掉工具结果与回答空间，
	// 因此对 prior 再做一次总量约束，超出时按「离当前步骤由近到远」保留。
	maxPriorBytes = 48 * 1024
)

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
```

保留 `truncateRunes` 供已有测试使用，但生产路径全部改走 `truncateBytes`。

---

## 10. 失败分级、重试与幂等

### 10.1 失败分级

```go
type FailKind int

const (
	FailTransient     FailKind = iota // 超时、429、5xx、连接重置
	FailPermanent                     // 工具不存在、参数非法、权限不足、404
	FailContradiction                 // 前提被证伪（来自 Violation，不是 error）
)
```

分类函数必须基于哨兵错误 + `errors.As`，**不要用字符串匹配**：

```go
var (
	ErrToolNotFound = errors.New("tool not found")
	ErrInvalidArgs  = errors.New("invalid tool arguments")
	ErrPermission   = errors.New("permission denied")
	ErrRateLimited  = errors.New("rate limited")
)
```

工具层如果现在没有这些哨兵错误，在 `internal/bot/tools` 里补，并把 `resilient.go` 现有的重试逻辑改为复用同一套判定。

### 10.2 重试策略

| 类型 | 重试 | 消耗 replan 配额 | 退避 |
|---|---|---|---|
| transient | 最多 `MaxRetries=1` 次 | 否 | 500ms，指数，±20% jitter |
| permanent | 不重试 | 是 | — |
| contradiction | 不重试 | 是 | — |

**这解决了 D5**：瞬时抖动不应该烧掉唯一的纠错机会。

### 10.3 幂等键

```go
// IdempotencyKey 由 run_id + step_id + 逻辑意图 决定，与 attempt 无关。
// 同一步骤的重试复用同一个 key；重规划产生的新步骤使用新 key。
func IdempotencyKey(runID string, stepID int, tool string, args []byte) string {
	h := sha256.New()
	h.Write([]byte(runID))
	fmt.Fprintf(h, ":%d:%s:", stepID, tool)
	h.Write(args)
	return hex.EncodeToString(h.Sum(nil))[:32]
}
```

- 通过 `context.WithValue` 下发（`planner.WithIdempotencyKey`），工具层读取。
- 服务端已有的 `message_id = uuid.NewSHA1(..., "logos:chat:"+senderID+":"+requestID)` 是同一思路，可复用其命名空间约定。
- 重放路径：`Effect == EffectSideEffect` 且已存在 `(run_id, step_id, input_hash)` 的成功记录 → 直接返回旧输出，不调用工具。

---

## 11. 持久化

### 11.1 位置

新增 `internal/service/ai/bot/model/plan.go`，并在 `model.AutoMigrate`（`internal/service/ai/bot/model/bot.go:167`）中注册。

DAO 新增 `internal/service/ai/bot/dao/plan_run.go`，实现 planner 侧定义的接口（依赖倒置，planner 包不依赖 gorm）。

```go
// planner 包内定义，dao 实现
type RunStore interface {
	CreateRun(ctx context.Context, run *RunRecord) error
	UpdateRun(ctx context.Context, runID string, patch RunPatch) error
	AppendStep(ctx context.Context, rec *StepRecord) error
	FindStepOutput(ctx context.Context, runID string, stepID int, inputHash string) (*StepRecord, error)
	LatestRun(ctx context.Context, conversationID string) (*RunRecord, error)
}
```

### 11.2 表结构

```go
type PlanRun struct {
	ID             string    `gorm:"primaryKey;size:36;comment:运行ID"`
	BotID          string    `gorm:"index;size:36;comment:BotID"`
	ConversationID string    `gorm:"index;size:100;comment:对话ID"`
	UserID         string    `gorm:"index;size:36;comment:用户ID"`
	Goal           string    `gorm:"type:text;comment:原始目标（不可变）"`
	GoalHash       string    `gorm:"size:64;index;comment:目标哈希"`
	Status         string    `gorm:"size:20;index;comment:running/completed/failed/degraded"`
	Revision       int       `gorm:"comment:重规划版本号"`
	PlanJSON       JSONBlob  `gorm:"type:jsonb;comment:当前计划快照"`
	Error          string    `gorm:"type:text;comment:终止原因"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

type PlanStepRecord struct {
	ID         string    `gorm:"primaryKey;size:36"`
	RunID      string    `gorm:"index:idx_step_lookup,priority:1;size:36"`
	StepID     int       `gorm:"index:idx_step_lookup,priority:2"`
	Revision   int       `gorm:"index:idx_step_lookup,priority:3"`
	Attempt    int       `gorm:"index:idx_step_lookup,priority:4"`
	Status     string    `gorm:"size:20"`
	Effect     string    `gorm:"size:20"`
	InputHash  string    `gorm:"size:64;index;comment:入参哈希，用于重放去重"`
	Output     string    `gorm:"type:text"`
	Facts      JSONBlob  `gorm:"type:jsonb"`
	Violations JSONBlob  `gorm:"type:jsonb"`
	TokensIn   int
	TokensOut  int
	DurationMs int
	Error      string    `gorm:"type:text"`
	StartedAt  time.Time
	FinishedAt time.Time
}
```

`JSONBlob` 自定义类型，实现 `driver.Valuer` / `sql.Scanner`，写法照抄同文件里的 `JSONMap`（`bot.go:112`），但底层用 `[]byte`。

索引说明：

- `idx_step_lookup (run_id, step_id, revision, attempt)`：按版本回放。
- `input_hash` 单列索引：跨版本重放查询。
- 不加 `deleted_at`。执行记录是审计数据，只归档不软删。

### 11.3 写入时机

| 时机 | 写入内容 | 是否阻塞主链路 |
|---|---|---|
| `Execute` 开始 | `PlanRun`（status=running） | 是（失败则整轮不执行，宁可不跑也不留脏数据） |
| 每步开始 | `PlanStepRecord`（status=running，started_at） | 否（异步，丢一条不影响正确性） |
| 每步结束 | 更新同一行（status/output/facts/tokens） | 是（这是 checkpoint，丢了就无法续跑） |
| 每次 patch | 更新 `PlanRun.plan_json` + `revision` | 是 |
| `Execute` 结束 | 更新 status + error | 是 |

### 11.4 续跑语义

新请求携带 `resume_run_id` 时：

1. 读 `PlanRun`，只允许 `status ∈ {running, failed}` 且 `updated_at` 在 10 分钟内。
2. 读该 run 全部 `PlanStepRecord`，按 `(step_id, revision, attempt)` 取每个步骤最新一条。
3. `done` 且 `Effect != side_effect` 的步骤直接复用输出，不重跑。
4. `side_effect` 的步骤即使 `done` 也**必须**用 `input_hash` 复核一次，防止"落库成功但响应丢失"造成重复副作用。
5. `running` 的步骤视为 `stale`（可能执行到一半进程挂了），重跑。

---

## 12. Prompt 全文

### 12.1 规划

```
你是任务规划器。把用户目标分解为可执行的步骤图。

规则：
1. 步骤数 1–%d。每步是一个原子子任务，能用现有工具（知识库检索、图谱查询、MCP 工具）或模型知识完成。
2. 步骤之间用 depends_on 声明依赖。没有真实数据依赖就不要连边，允许并列。
3. 每步必须给出 assumptions：这一步成立所依赖的、可能被后续步骤推翻的前提。
   写具体事实，不要写"信息充足"这类空话。
4. 每步给出 expects：一句话描述预期产出，用于判断它是否真的完成。
5. effect 取值：none（只读）、idempotent（可重复的写）、side_effect（发消息/通知/扣款等不可重复操作）。
6. 不要为了凑步骤而拆分。一个步骤能完成就输出一个。

只输出 JSON：
{"goal":"目标复述","steps":[
  {"id":1,"description":"...","tool":"...","depends_on":[],
   "assumptions":["..."],"expects":"...","effect":"none"}
]}
```

### 12.2 重规划（patch）

```
你是任务规划器。当前计划执行中出现了失效，需要局部修订。

规则：
1. 只输出 operations，不要输出完整计划。
2. 已完成且未失效的步骤不要动，它们的结果会被复用。
3. 用 replace_from 替换从某步开始的整条链，用 modify 做小幅修正，用 add 补充新步骤。
4. 新步骤用 local_id 标识（字符串），depends_on 可以引用全局数字 ID 或 local_id。
5. 不要修改目标。不要输出 reset。
6. 在预算内完成：剩余 %d 次模型调用、%d 秒。

只输出 JSON：
{"reasoning":"...","operations":[
  {"op":"replace_from","id":2,"steps":[{"local_id":"a","description":"...","depends_on":[],"effect":"none"}]}
]}
```

### 12.3 前提校验

```
下面是一组已知事实和一条待校验的前提。判断该前提是否被已知事实支持。

已知事实：
%s

待校验前提：%s

只输出 JSON：{"holds":true,"reason":"..."}
不确定时输出 false。
```

`不确定时输出 false` 是刻意的：漏判的代价是"少一次纠错"，误判的代价是"作废正确结果并重跑"，后者更贵。

### 12.4 综合

```
你是任务综合器。根据步骤结果回答用户目标。

规则：
1. 直接给答案，不要复述执行过程。
2. 标记为【已失效】的步骤结果不得作为依据，即使它看起来相关。
3. 如果有步骤失败或未执行，明确说明哪部分没有完成，不要用推测填补。
4. 不要输出"根据步骤 3 的结果"这类内部用语。
```

`FormatResults` 需要在失效步骤前加 `【已失效】` 前缀，并从 prompt 中移除其正文（只留一句"该步骤结果已失效"），避免模型"看见了就忍不住用"。

---

## 13. 配置

新增到 `config.Config`（`config/config.go:22`）：

```go
type Planner struct {
	Enabled        bool          `mapstructure:"enabled"`
	Mode           string        `mapstructure:"mode"` // legacy | dag，默认 legacy
	MaxSteps       int           `mapstructure:"max_steps"`
	MaxReplans     int           `mapstructure:"max_replans"`
	MaxConcurrency int           `mapstructure:"max_concurrency"`
	MaxLLMCalls    int           `mapstructure:"max_llm_calls"`
	MaxWallClock   time.Duration `mapstructure:"max_wall_clock"`
	VerifyAssumes  bool          `mapstructure:"verify_assumptions"`
	VerifyExpects  bool          `mapstructure:"verify_expects"`
	PersistRuns    bool          `mapstructure:"persist_runs"`
}
```

默认值集中在 `planner.DefaultConfig()`，配置缺省时用它，不要在 `NewPlanner` 里散落 `if x == 0 { x = ... }`。

灰度：`Mode=legacy` 时走现有 `ExecutePlan`，`dag` 时走新 `Execute`。两条路径共享 `ParsePlan / ValidatePlan` 的公共部分，避免行为漂移。

---

## 14. 可观测性

### 14.1 Metrics（`pkg/obs` 已有封装，按现有风格注册）

| 名称 | 类型 | 标签 |
|---|---|---|
| `planner_runs_total` | counter | `status`（completed/failed/degraded/budget/blocked） |
| `planner_steps_total` | counter | `status`, `effect` |
| `planner_step_duration_seconds` | histogram | `effect` |
| `planner_replans_total` | counter | `reason`（permanent/contradiction/budget） |
| `planner_stale_steps_total` | counter | — |
| `planner_budget_exhausted_total` | counter | `kind`（calls/tokens/wallclock） |
| `planner_llm_calls_per_run` | histogram | — |

### 14.2 日志字段

统一带：`run_id`、`revision`、`step_id`、`attempt`、`effect`、`stale_reason`、`fail_kind`。

关键节点各一条，不要每步打三条：

- 计划生成：`steps`、`edges`、`max_depth`
- 步骤结束：`status`、`duration_ms`、`tokens_in/out`
- 失效传播：`seeds`、`stale`、`propagated_count`
- patch 应用：`ops`、`added`、`removed`、`modified`
- 预算耗尽：`kind`、`consumed`、`limit`

---

## 15. 测试

### 15.1 必须继续通过（回归红线）

```
TestParsePlan_ValidJSON
TestParsePlan_ToleratesMarkdownFence
TestParsePlan_TrimExtraText
TestValidatePlan
TestPlanner_CreatePlan
TestPlanner_ExecutePlan_AllStepsSucceed
TestPlanner_ExecutePlan_PassesPriorResultsToNextStep
TestPlanner_ExecutePlan_FailureTriggersReplan
TestPlanner_ExecutePlan_ReplanOnlyOnce
TestBuildStepPrompt
TestFormatResults
TestTruncateRunes_ChineseNotCut
TestFormatResults_TruncatesByRune
```

### 15.2 新增用例（表格驱动）

| # | 用例 | 断言 |
|---|---|---|
| T1 | 无环 DAG，2 层，4 步 | 层内并发执行，总耗时 < 串行 |
| T2 | 自环 `depends_on:[self]` | `ValidatePlan` 返回 error |
| T3 | 环 A→B→A | `ValidatePlan` 返回 error，错误信息含环路径 |
| T4 | 依赖不存在的 ID | `ValidatePlan` 返回 error |
| T5 | 第 7 步返回 `violations:[{step_id:2}]` | step2 及其全部下游 `stale`；不在下游的步骤结果复用 |
| T6 | violation 指向不存在的 step_id | 忽略并 warn，执行继续 |
| T7 | transient error 一次后成功 | 共 2 次 attempt，`replans == 0` |
| T8 | permanent error | 不重试，`replans == 1` |
| T9 | 连续 3 次失败 | `replans <= MaxReplans`，最终 `status=degraded`，不 panic |
| T10 | patch 的 `replace_from` 指向中间步骤 | 该步及全部下游 `skipped`，新链正确接上 |
| T11 | patch 引用了不存在的 id | 整个 patch 回滚，plan 与 patch 前逐字段相等 |
| T12 | patch 产生环 | 整个 patch 回滚 |
| T13 | `MaxLLMCalls=3` | 第 4 次调用前终止，`status=budget_exhausted` |
| T14 | 32 个并发 `onProgress` 事件 | 事件顺序单调，无 race（`-race`） |
| T15 | `EffectSideEffect` 步骤重放 | 第二次直接返回旧输出，工具调用计数不增加 |
| T16 | `truncateBytes` 切断多字节字符 | 输出是合法 UTF-8，且长度 ≤ max+len("...(截断)") |
| T17 | `maxStepOutputBytes` 生效 | 20KB 中文输出被截到 16KB 附近，误差 < 4 字节 |
| T18 | 从 checkpoint 续跑 | 已完成只读步骤不重复调用，调用计数等于剩余步骤数 |

### 15.3 测试基建

- 现有 `chatRecorder`（`planner_test.go`）扩展成 `scriptedChat`：按调用序返回预设响应，记录每次 prompt，支持断言"prompt 里包含/不包含某段"。
- 新增 `fakeStore`：内存实现 `RunStore`，支持注入失败（验证落库失败时的行为）。
- 新增 `steppingExecutor`：可编程延迟、可编程 error、可编程 outcome，用于并发与超时用例。
- 所有新测试必须能在 `go test -race ./internal/bot/planner/...` 下通过。

### 15.4 黄金用例（必须逐字段断言）

场景：8 步计划，step2 声称"文件为 CSV"，step7 发现"实际为 XLSX，含合并单元格"。

期望：

1. step2 与 step7 之间存在依赖路径（2→3→7）；
2. step7 的 violation 指向 step2，`seeds = [2]`，不是 `[7]`；
3. `stale = {2,3,7}` 及 2、3 的所有下游；
4. 不在下游的 step4/5/6/8 结果被复用，各只执行 1 次；step2/3/7 在失效前各执行 1 次，
   patch 后由新链替换而不是重复执行。总 `exec` 次数 = 原 8 次 + 新链步骤数；
5. 只发生 1 次 replan；
6. 最终 `Synthesize` 的 prompt 中，step2/3/7 的正文均不出现。成功应用 patch 时，
   被替换的失效分支整体不进入 prompt；无法重规划（`degraded`）时，失效步骤以
   "【已失效】"占位，正文仍然被剔除。

---

## 16. 分阶段落地

### P0（半天，先止血，可立即合入）

1. 修 `truncateRunes` 调用点 → 改为 `truncateBytes`，修正 16KB 语义（D8）。
2. `replans` 改为按步骤计数，或明确为全局并写清注释（D5）。
3. 冻结 `plan.Goal`：`Replan` 返回后强制 `newPlan.Goal = originalGoal`（D6）。
4. `Replan` 传 `allResults` 而非 `results`（D7）。
5. `Synthesize` 前过滤失败步骤的错误文本（D10）。
6. 补 `truncateBytes` 的两个单测（T16/T17）。

**验收**：现有 13 个测试全绿 + 新增 2 个；`git diff` 不触碰任何架构代码。

### P1（1–2 天，能力骨架）

1. `PlanStep` 加 `DependsOn / Assumptions / Expects / Effect`。
2. `ValidatePlan` 加环检测、引用完整性、effect 合法性。
3. `StepOutcome` + 结构化尾块协议；`StepExecutor` 改签名，保留旧签名适配器。
4. 顺序执行下实现 dependency-aware 调度（仍是单线程，但按拓扑序）。
5. 失效传播 `Propagate` + `stale` 状态 + `Synthesize` 过滤。
6. 黄金用例 T5 跑通。

**验收**：T1–T7 通过；黄金用例逐字段断言通过。

### P2（2–3 天，并发与 patch）

1. 层内并发调度 + 副作用串行 + 并发上限。
2. `ProgressEvent` + 事件串行化。
3. `PlanPatch` 协议 + `ApplyPatch` + 回滚。
4. `MaxReplans=2`，重规划 prompt 带预算与失效集合。
5. 失败分级 + 重试（`FailKind`）。

**验收**：T8–T14 通过；`go test -race` 通过。

### P3（3–5 天，可恢复与安全）

1. `PlanRun` / `PlanStepRecord` 模型 + DAO + 接口。
2. checkpoint 写入与续跑。
3. 幂等键下发 + 副作用重放查询。
4. metrics + 结构化日志。
5. `Mode=legacy|dag` 灰度开关。

**验收**：T15–T18 通过；杀掉进程后同 `run_id` 续跑，只读步骤调用计数为 0。

---

## 17. 验收标准

全部满足才算完成：

1. `go build ./...` 通过。
2. `go test ./internal/bot/planner/... ./internal/service/ai/bot/...` 通过。
3. `go test -race ./internal/bot/planner/...` 通过。
4. 13 个既有测试零修改通过（允许新增，不允许改断言）。
5. 黄金用例（§15.4）逐字段断言通过。
6. `Mode=legacy` 时行为与改动前一致（用同一组 scriptedChat 响应做 diff）。
7. 任意 `MaxLLMCalls` 取值下不 panic、不无限循环、必然返回（T13 覆盖）。
8. 所有新增公开函数有 doc comment，说明 why。
9. 没有引入新的第三方依赖。

---

## 18. 禁区

以下做法一律不接受，评审会直接打回：

1. **用 `time.Sleep` 或轮询实现步骤等待**。用 channel / WaitGroup。
2. **用字符串匹配判断错误类型**。用哨兵错误 + `errors.As`。
3. **在 planner 包里 import gorm**。持久化通过接口注入。
4. **让模型决定全局 step ID**。ID 必须由系统单调分配。
5. **patch 失败后"尽力应用部分操作"**。必须整体回滚，否则 plan 处于半坏状态。
6. **并发写 `onProgress` 不加锁**。前端会出现交错文本。
7. **把 `stale` 直接改成 `running`**。必须经过 `pending → ready`，否则跳过前提重校验。
8. **在 `Execute` 里 `recover()` 吞掉 panic**。panic 应上抛，由上层统一处理并记录。
9. **为了通过测试修改既有断言**。

---

## 19. 附录

### 19.1 现有代码关键位置

```
internal/bot/planner/plan.go:53      MaxPlanSteps
internal/bot/planner/plan.go:55      MaxReplanCount
internal/bot/planner/plan.go:56      maxStepOutputKB
internal/bot/planner/plan.go:74      ValidatePlan
internal/bot/planner/plan.go:129     truncateRunes
internal/bot/planner/plan.go:147     truncateRunes 误用（D8）
internal/bot/planner/planner.go:43   CreatePlan
internal/bot/planner/planner.go:53   Replan
internal/bot/planner/planner.go:67   Synthesize
internal/bot/planner/planner.go:84   ExecutePlan
internal/bot/planner/planner.go:86   replans 全局计数（D5）
internal/bot/planner/planner.go:108  Replan 只传本轮结果（D7）
internal/bot/planner/planner.go:121  runSteps
internal/bot/planner/planner.go:156  BuildStepPrompt
internal/bot/planner/planner.go:180  truncateForContext（D8）
internal/service/ai/bot/service/bot.go:235  planner 装配点
internal/service/ai/bot/service/bot.go:260  chatWithPlanner
internal/service/ai/bot/service/bot.go:949  非流式降级
internal/service/ai/bot/service/bot.go:1166 流式降级
internal/service/ai/bot/model/bot.go:167    AutoMigrate 注册点
internal/service/ai/bot/model/bot.go:112    JSONMap 实现（JSONBlob 照抄）
```

### 19.2 术语

| 术语 | 含义 |
|---|---|
| stale | 步骤已有结果，但因上游失效而作废，需要重跑 |
| seed | 触发失效传播的源头步骤（失败或上报 violation 的步骤） |
| patch | 对现有计划的增量修改，不是完整计划 |
| revision | 计划被 patch 的次数，从 0 开始 |
| attempt | 同一 `(step_id, revision)` 下的重试序号，从 0 开始 |
| checkpoint | 步骤结束后落库的执行记录，用于续跑与审计 |
| effect | 步骤的副作用等级，决定能否重放与并行 |

### 19.3 一句话定位（给评审/面试用）

> 这是一个带失效传播的 Plan-and-Execute 执行器：步骤显式声明前提与依赖，执行中把"前序结论被推翻"作为一等信号上报，沿依赖图定位需要重跑的步骤，用 patch 而非重写来修订计划，未受影响的结果直接复用；副作用步骤带幂等键，执行记录落库以支持中断续跑。

---

## 20. 实现状态（2026-11-06）

### 20.1 已落地

| 能力 | 位置 |
|---|---|
| DAG 数据模型、状态机、环/引用/effect 校验 | `internal/bot/planner/plan.go` |
| 分层并发调度、副作用串行、预算、失败分级与重试 | `internal/bot/planner/execute.go` |
| 失效传播与 `stale`（含未知 `step_id` 忽略并告警） | `plan.go`、`execute.go` |
| patch 协议 `add / modify / remove / replace_from`，clone 应用 + 整体回滚 | `internal/bot/planner/patch.go` |
| 运行记录接口、输入指纹、续跑窗口、`PlanStats` | `internal/bot/planner/store.go` |
| 规划 / 重规划 / 前提与产出校验 / 综合 prompt | `internal/bot/planner/planner.go` |
| 持久化模型与 DAO（`plan_runs` / `plan_step_records`） | `internal/service/ai/bot/model/plan.go`、`internal/service/ai/bot/dao/plan_run.go` |
| `Mode=legacy|dag` 与 per-Bot 覆盖、`disable_replan`、`persist_runs` | `internal/service/ai/bot/service/bot.go`、`config/config.go` |
| 指标与结构化日志 | `pkg/obs/metrics.go`、`execute.go` |

### 20.2 已验证

```text
go build ./...                                       通过
go test ./internal/bot/planner/... -count=1          通过
go test -race ./internal/bot/planner/... -count=1    通过
go test ./internal/service/ai/bot/service/...        通过
go test ./... -count=1                               通过
go vet ./internal/bot/planner/...                    通过
```

§15 用例落点（均为可执行断言，不是人工检查）：

| 用例 | 测试 |
|---|---|
| T1 | `TestExecute_DAGOrderAndParallelism`、`TestExecute_ParallelLayerPreReserve` |
| T2/T3/T4 | `TestValidatePlan_DependencyGraph`（含环路径 `1 -> 2 -> 1`） |
| T5 | `TestExecute_ViolationPatchReusesUnaffectedResults`、`TestExecute_GoldenInvalidationPropagation` |
| T6 | `TestExecute_UnknownViolationIsIgnored` |
| T7 | `TestExecute_TransientRetryDoesNotReplan` |
| T8 | `TestPlanner_ExecutePlan_FailureTriggersReplan` |
| T9 | `TestExecute_ReplanBudgetTerminatesDegraded` |
| T10 | `TestExecute_GoldenInvalidationPropagation`（`replace_from` 指向中间步骤） |
| T11 | `TestApplyPatch_UnknownOperationKeepsPlan` |
| T12 | `TestApplyPatch_CycleRollsBackWholePlan` |
| T13 | `TestExecute_BudgetExhausted`、`TestExecute_ExactBudgetStillCompletes` |
| T14 | `TestExecute_ProgressEventsAreOrdered`（`-race`） |
| T15 | `TestExecuteWith_ReplaysCompletedSideEffect` |
| T16/T17 | `TestTruncateBytes_MultibyteBoundary`、`TestFormatResults_TruncatesByBytes` |
| T18 | `TestExecuteWith_ResumeSkipsCompletedReadOnlySteps` |

额外覆盖了 patch 修改已完成步骤时下游重跑、token 预算覆盖步骤 prompt、失败正文不进入综合 prompt、取消时仍写终态、验证失败 fail-open 等边界。

§7.1 路径 B、§9.3 降级阶梯、§11.3 checkpoint 与步数入口约束的收口测试：

| 用例 | 测试 |
|---|---|
| 前提被推翻 → blocked → 上游失效 → patch 改写 | `TestExecuteWith_AssumptionViolationBlocksAndReplans` |
| 新计划超过 8 步在入口被拒绝，且不建运行记录 | `TestExecuteWith_RejectsOversizedInitialPlan` |
| patch 累积到 9 步的续跑不被入口上限误伤 | `TestExecuteWith_ResumeAllowsPatchedStepCount` |
| running/final 合并为同一行 checkpoint | `TestExecuteWith_CheckpointUpdatesSameRow` |
| 降级顺序与只降不升 | `TestNextDegrade_FollowsLadder` |

### 20.3 尚未完成 / 已知限制

| 项 | 状态 | 说明 |
|---|---|---|
| `AvgReActCalls` 真实分布回填 | 未执行 | 预算推导仍按 3 估算，需用 `planner_llm_calls_per_run` 的生产分布校准 |
| 持久化 DAO 集成测试 | 未执行 | `dao.NewPlanRunStore` 依赖真实 PostgreSQL；续跑语义用内存 `fakeStore` 覆盖，DAO 的 SQL 与索引尚未在 CI 中跑过 |
| 跨进程崩溃续跑 | 未执行 | 单测验证了"从 checkpoint 恢复"的逻辑；"kill -9 后重启续跑"需要部署环境验证 |
| 长稳压测 | 未执行 | 未测量分层并发在真实工具延迟下的收益，不得填数 |
| 线上灰度 | 未开启 | 代码默认 `mode=legacy`，`planner.enabled=false`；`dag` 与 `persist_runs` 均需显式打开 |

### 20.4 规范差异的收口情况

原表中列出的实现取舍已经收口，行为与规范一致；这里记录"改成了什么"与"用什么证明"。

| 规范 | 当前实现 | 验证 |
|---|---|---|
| §7.1 路径 B：前提不成立标 `blocked`，并把 `violated` 转成 `Violation` | `running -> blocked` 合法；`violated` 的 1-based 下标映射到 `DependsOn` 对应上游；blocked 同时作为失效种子；patch 后用 `ResetBlocked` 放回 `ready` 重新调度；未发生的调用/token 预留全部退还 | `TestExecuteWith_AssumptionViolationBlocksAndReplans`、`TestParseVerifyResponse_ProtocolCompatibility` |
| §9.3 按"降并发 → 关路径 C → 关重规划 → 部分结果 → 抛错"逐级降级 | `execState.nextDegrade` 单调推进 1–4 级，只改本轮局部 `Config`；软压力降一级后重试，硬预算（含"单步 prompt 都放不下"）走完阶梯后返回 `budget_exhausted`；`DegradeStage` 进入结果与日志 | `TestNextDegrade_FollowsLadder`、`TestExecute_TokenBudgetCoversStepPrompt` |
| §11.3 每步开始异步写 `running`，结束时阻塞更新同一行 | `beginStepCheckpoint` 用 `context.WithoutCancel` 异步 INSERT；终态等待 INSERT 完成，优先走可选接口 `StepUpdater.UpdateStep` 原地更新，仓库不支持或更新失败时回退追加；DAO 已实现 `UpdateStep` | `TestExecuteWith_CheckpointUpdatesSameRow` |
| §6.2 单次生成 ≤ 8 步，patch 累积 ≤ 12 步 | 非续跑新计划在创建运行记录前按 `min(cfg.MaxSteps, MaxPlanSteps)` 校验；续跑计划 hydrate 后按 `MaxTotalSteps` 校验，允许 patch 累积到 9–12 步 | `TestExecuteWith_RejectsOversizedInitialPlan`、`TestExecuteWith_ResumeAllowsPatchedStepCount` |

仍未执行的仍是部署与标定类工作，不是代码缺口：跨进程 `kill -9` 续跑未在真实环境验证、DAO SQL 未进 CI、预算阈值与分层并发收益未用生产数据标定（见 §20.3）。面试口径应说"单测覆盖了这些语义，线上数据尚未标定"，不要说成已经在生产验证。



