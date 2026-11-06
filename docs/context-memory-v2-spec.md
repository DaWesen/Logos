# Context Memory v2 规格：从单池预算到四层上下文内存

> 交接对象：负责实现的下游 AI / 工程师
> 代码基线：`Logos` 仓库当前 `main` 分支
> 目标包：`internal/service/ai/bot/service`（上下文组装）、`internal/bot/agent`（消费侧）、`internal/bot/memory`（长期记忆，仅接线）
> 要求：按 P0 → P4 分阶段落地，每阶段可独立合入、独立回滚、独立验证。

> **实现状态（2026-11-06）**
>
> - **已实现（P0 全部）**：D1 长期记忆改为 per-user 消息注入、D2 连续同角色消息合并、D3 rune-safe 截断、D4 分页回溯历史（100 条/页，最多 500 条，目标预算 6000 + 2000 token）。
> - **回归测试**：`TestToSchemaMessages_MergesConsecutiveUserMessages`、`TestRenderMessages_TruncatesChineseByRune`、`TestMemoryContextMessages_IsPerRequestUserPair`、`TestCollectHistoryPages_*`。
> - **未实现**：P1–P4 全部内容。
> - **已执行验证（收尾）**：`go build ./...`、`go test ./internal/service/ai/bot/service/... ./internal/bot/agent/...`、`go vet ./internal/service/ai/bot/service/... ./internal/bot/agent/...` 全部通过。
> - §1、§2 与附录 A 描述的是 **P0 改造前**的基线；本文其余数字与分层仍是设计目标，不是已上线事实。

---

## 0. 交接须知（先读这一节）

1. 这是**增量改造**，不是重写。现有 `context_compression_test.go`、`context_budget_test.go`、`memory` 包下 9 个测试文件必须继续通过（见 §15）。
2. **不要**引入向量数据库、HNSW、新的编排框架。会话内语料量级是 10²–10³ 条消息，倒排 + 暴力打分足够；引入向量库会把一个 2 天的工作变成 2 周，且没有任何可测收益。
3. **不要**修改消息链路、Outbox、Kafka、Redis PubSub 相关代码。本规格作用域只有：上下文组装、摘要、槽位抽取、会话内检索，以及新增的持久化模型。
4. 与另外两份 spec 的边界：`planner-v2-spec.md` 管执行图，`semantic-cache-v2-spec.md` 管答案复用，本规格管**进入模型之前的上下文**。三者共享 `estimateTokenCount` 的口径，除此之外互不侵入。
5. 每阶段结束必须能回答："如果现在回滚，用户会看到什么变化？"答不上来说明改动没有隔离好。
6. 代码注释用中文，解释 **why** 不解释 **what**，与现有风格一致。
7. 文中所有数字都是**设计目标**，不是实测值。任何要写进代码注释或简历的数字，必须能用 §13 的指标或 §15 的测试复现。

---

## 1. 现状

### 1.1 能力地图

| 能力 | 位置 | 说明 |
|---|---|---|
| 历史拉取 | `internal/service/ai/bot/service/bot.go:1150`、`:1396` | `repo.GetMessages(ctx, convID, 60, nil)`，固定最近 60 条 |
| 预算装载 | `internal/service/ai/bot/service/context_compression.go:187` | `packMessagesByBudget`，按 token 从新往旧装 |
| 滚动摘要 | `context_compression.go:107` | 溢出部分增量摘要，进程内缓存 |
| 摘要 prompt | `context_compression.go:40` | 300 字自由散文，无结构约束 |
| 消息转 Schema | `context_compression.go:210` | 只保留 user/assistant，丢弃 tool |
| 第二次裁剪 | `internal/bot/agent/context_budget.go:39` | 12000 **字符**，从新往旧砍 |
| 省略提示 | `context_budget.go:81` | `historyNotice(omitted)` 告知模型有省略 |
| 长期记忆注入 | `bot.go:1788` → `:2044` | 按 (userID, botID) 检索，拼进 system prompt |
| 记忆打分 | `internal/bot/memory/retrieval.go:36` | 相关性 0.5 / 新鲜度 0.3 / 重要性 0.2，上限 16 条 |
| 记忆置信门槛 | `internal/bot/memory/merge.go:135` | `memoryMinConfidence = 0.5` |
| 工具结果预算 | `internal/bot/toolbudget/budget.go:22` | `DefaultTurnBudget = 48000` 字符/轮 |
| 单工具截断 | `internal/bot/tools/result_truncate.go:13` | 保头 60% / 保尾 40%，中间折叠 |

### 1.2 一次请求的上下文组装全链路

```
SendMessage
  ├─ semanticCache.Lookup ──────────────────────► 命中直接返回（不进上下文）
  ├─ getOrCreateAgentWithMemory(bot, userID, query)
  │    ├─ buildMemoryEnhancedSystemPrompt        ← 用户记忆拼进 system prompt
  │    └─ agentManager.GetOrCreateAgent(bot.ID)  ← 按 bot.ID 缓存 Agent
  ├─ compressor.BuildContextMessages
  │    ├─ repo.GetMessages(limit=60)             ← 硬窗口
  │    ├─ packMessagesByBudget(6000 token)       ← 只算 Content
  │    ├─ summarizeOverflow → 300 字散文
  │    └─ toSchemaMessages                       ← 同角色去重 / 丢 tool
  ├─ DetectPromptInjection → 可能再插一对消息
  └─ botAgent.ChatWithHistory
       └─ buildMessages → trimHistory(12000 字符) ← 第二次裁剪
            └─ system + trimmed history + current
```

关键：**同一次请求里，历史被两套不同单位、不同口径的预算各裁一次**。

### 1.3 关键常量

```go
historyTokenBudget      = 6000   // context_compression.go:30，token
historyFetchCount       = 60     // context_compression.go:34，条
minOverflowToSummarize  = 4      // context_compression.go:35，条
historyRuneBudget       = 12000  // context_budget.go:16，字符
DefaultTurnBudget       = 48000  // toolbudget/budget.go:22，字符
memoryPromptLimit       = 16     // memory/retrieval.go:19，条
memoryMinConfidence     = 0.5    // memory/merge.go:135
```

### 1.4 已有的好东西（不要推翻）

以下设计是对的，本改造只是把它们拆到正确的层里：

- **滚动摘要 + 增量合并**：只对上次摘要点之后的内容做一次合并，均摊成本 O(溢出条数)。
- **摘要失败降级到截断**：主流程不被摘要拖死。
- **省略告知**：`historyNotice(omitted)` 让模型知道"前面还有内容"，比静默截断诚实得多。
- **记忆的三因子检索**：相关性/新鲜度/重要性，思路来自 Generative Agents，方向正确。
- **手工记忆置顶**：`Source == "manual"` 跳过打分强制保留，这是产品级细节。
- **记忆双结论**：`ConflictValue` 字段允许保留矛盾旧值而不自动选边。
- **工具结果保尾截断**：结论常在末尾，这个直觉是对的。

---

## 2. 缺陷清单

严重度定义：
- **S1**：正确性或安全缺陷，会直接给出错误答案或泄漏数据，必须优先修。
- **S2**：结构性缺陷，当前不必然出错，但在真实输入下会稳定劣化。
- **S3**：可维护性/可观测性缺陷，不影响单次结果，影响定位与演进。

### S1

#### D1　记忆跨用户泄漏

- 位置：`bot.go:1788`（按 userID 构造增强 prompt）→ `bot.go:1794`（写进 `cfg.SystemPrompt`）→ `manager.go:90`（按 `cfg.ID` 缓存）
- 现象：`cfg.ID = bot.ID`，Agent 按 bot 缓存。**第一个**访问该 Bot 的用户，其长期记忆被固化进 Agent 的 system prompt；此后所有用户复用同一个 Agent 实例，看到的是第一个用户的记忆。
- 根因：把「per-user 的运行时数据」塞进了「per-bot 的构建期配置」。
- 影响：隐私泄漏；用户 B 问"我的口味"会得到用户 A 的答案。这是本清单里唯一的安全级缺陷。
- 附加：`MaxIterations: 10`（`bot.go:1795`）同样被写死，覆盖了 `AgentConfig` 的可配置能力。

#### D2　连续同角色消息被静默丢弃

- 位置：`context_compression.go:216`
- 代码：`if lastRole == "user" { continue }`
- 现象：用户在 IM 里连发两条（"订单号 20261106001" / "帮我查下"），第一条被直接删除，不报错、不提示、不进摘要。
- 影响：IM 场景下连发是常态而非边缘情况，这是最高频的丢失路径。

#### D3　按字节切片，中文被切坏

- 位置：`context_compression.go:241-242`
- 代码：`if len(content) > 500 { content = content[:500] + "..." }`
- 现象：`len()` 与切片对 string 都是**字节**语义。中文 3 字节/字，500 字节 ≈ 166 字；且切片点可能落在 UTF-8 多字节序列中间，产生非法字符串送进摘要模型。
- 影响：长消息尾部（常放编号、结论）被切掉；偶发乱码。

#### D4　60 条硬窗口使摘要机制可能永不触发

- 位置：`bot.go:1150`、`:1396` → `context_compression.go:87`
- 现象：只拉最近 60 条。若这 60 条的总 token 小于 6000，`overflow` 恒为空，`summarizeOverflow` 永不执行。第 61 条以前的消息既没有原文、也没有摘要，**完全不可达**。
- 影响：摘要给人"我在保底"的错觉，实际它只在一个从未发生的条件下工作。

### S2

#### D5　两套预算单位不一致

- 压缩层：6000 **token**（`context_compression.go:30`）
- Agent 层：12000 **字符**（`context_budget.go:16`）
- 中文：6000 token ≈ 1 万汉字，塞得进 12000 字符。
- 英文：6000 token ≈ 2.4 万字符，会被 Agent 层二次腰斩，且用户无感。
- 影响：同一份历史被两把不同刻度的尺子量两次，行为随语言漂移。

#### D6　压缩层不计 tool_calls

- 位置：`context_compression.go:190`，`cost := estimateTokenCount(m.Content)`
- 对照：Agent 层 `messageRunes`（`context_budget.go:20`）**计入**了 `tc.Function.Name` 与 `tc.Function.Arguments`。
- 影响：压缩层系统性低估占用，压缩触发得比预期晚；两层口径不一致导致"压缩层觉得装得下、Agent 层装不下"。

#### D7　摘要为自由散文，不保结构化事实

- 位置：`context_compression.go:40`
- 现象：prompt 只有"保留关键事实""300 字以内"，没有"编号/金额/日期必须原样保留"。
- 影响：LLM 会把 `20261106001` 概括成"一个订单号"，这是摘要的默认行为而非模型缺陷。

#### D8　摘要状态进程内

- 位置：`context_compression.go:54`，`states map[string]*summaryState`
- 影响：重启即失忆（降级为直接截断）；网关多副本时各节点各算各的摘要，同一会话在不同节点看到不同背景。

#### D9　摘要失败静默降级，无指标

- 位置：`context_compression.go:107` 之后的 error 分支
- 现象：`chat()` 返回 error → `return ""` → 溢出的消息直接消失。
- 影响：只有一条 `logger.Warn`，没有计数器，线上无法知道"今天有多少会话的记忆是断的"。

#### D10　无会话内检索

- 现象：50 轮以前的原文没有任何读路径。用户说"我上次说的那个"，模型只能靠摘要里的散文猜。
- 影响：这是"长对话越聊越笨"的直接来源。

#### D11　事实与叙述混在同一层

- 现象：订单号、金额、日期和"用户比较着急"这种叙述，竞争同一个 300 字额度。
- 影响：保真要求差一个数量级的两类信息，用同一套压缩策略处理，必然牺牲前者。

#### D12　预算硬编码，不随模型窗口变化

- 现象：换 32k / 128k / 200k 模型，6000 与 12000 两个常量毫无反应。
- 影响：小窗口模型会 400，大窗口模型浪费 90% 的可用上下文。

#### D13　注入内容无溯源

- 现象：注入的历史、摘要、记忆都不带"来自哪一轮/哪条消息"。
- 影响：答错时无法归因；无法做"用户否定了这条记忆"的定向修正。

#### D14　摘要与记忆语义重叠

- 现象：摘要里可能有"用户在上海"，system 里的长期记忆也可能有"用户在上海"，两者冲突时模型没有裁决依据。
- 影响：模型看到两个互相矛盾的事实，倾向选择更近的那个——但注入顺序上摘要更靠前，可能反而更"权威"。

### S3

#### D15　无上下文组装指标

- 现状：`pkg/obs/metrics.go` 有 `RecordLLMParse`（`:541`），但没有 token 分布、丢弃量、命中率。
- 影响：所有优化都无法证明有效。

#### D16　双次裁剪用户无感

- 现象：压缩层裁一次，Agent 层再裁一次，第二次的 `historyNotice` 只报告第二次的省略数。
- 影响：用户与开发者都无法知道"总共丢了多少"。

#### D17　摘要长度无 token 级校验

- 现象：prompt 写"300 字以内"，代码不校验。模型输出 800 字也照单全收，且下一轮还会被再次喂进合并 prompt。
- 影响：摘要长度随轮次漂移，最终吃掉预算。

#### D18　`renderMessages` 同样按字节截断

- 位置：`context_compression.go:233`
- 现象：与 D3 同源，`content[:500]`。
- 影响：摘要输入本身就被切坏，摘要质量进一步下降。

#### D19　`MaxIterations` 硬编码

- 位置：`bot.go:1795`
- 影响：`AgentConfig.MaxIterations` 形同虚设，所有 Bot 固定 10 轮。

#### D20　记忆管理器全局单例

- 位置：`memory/memory.go` 的 `memoryOnce sync.Once`
- 现象：第一次调用时的 `repo` 被永久捕获，后续传入新 repo 被忽略；`states` 同样在进程内。
- 影响：事务内调用会拿到旧连接；多副本下提取进度不共享。

---

## 3. 设计目标与非目标

### 3.1 目标

| 编号 | 目标 | 可验证方式 |
|---|---|---|
| G1 | 长会话中结构化事实（编号/金额/日期）不丢 | §15 T7、T8 |
| G2 | 组装后的 prompt 不超过模型窗口 | §15 T1（property test） |
| G3 | 上下文丢弃可解释、可观测 | §13 指标 + §15 T12 |
| G4 | 跨用户零泄漏 | §15 T5 |
| G5 | 同一输入产生同一组装结果 | §15 T9 |
| G6 | 任何一层失败都不影响请求成功 | §11 矩阵 + §15 T10 |
| G7 | 预算随模型窗口自适应 | §15 T2 |

### 3.2 非目标

- 不做跨会话的用户画像升级（那是 `memory` 包的职责，本规格只接线）。
- 不做 prompt 自动优化或自博弈。
- 不做多模态消息（图片/语音）的预算核算，但类型上留位。
- 不做分布式摘要一致性协议，只做"单调游标 + 后写覆盖前写"的弱一致。

---

## 4. 不变量

实现任何一层时，以下七条是硬约束。违反任意一条，宁可不做这层。

| 编号 | 不变量 | 违反后果 |
|---|---|---|
| I1 | 组装后 `Σ tokens ≤ H`，硬保证 | 供应商 400，或输出被截断 |
| I2 | 不产生孤立 tool 消息（tool 结果必须有其 assistant tool_calls 在前） | 供应商 400 |
| I3 | 注入的每条结构化事实必须能回溯到具体消息 | 幻觉无法证伪 |
| I4 | 任何 per-user 内容不得进入跨用户共享的构建期对象 | 隐私泄漏（D1） |
| I5 | 相同输入 + 相同状态 → 相同组装结果 | 无法回放、无法单测 |
| I6 | 摘要游标 `upToMsgID` 单调递增 | 摘要重复或倒退 |
| I7 | 任一层失败 → 退化为"少一层"的组装，请求仍成功 | 一次摘要超时拖垮整个对话 |

---

## 5. 领域模型

新增包：`internal/bot/ctxmem`。以下是核心类型，实现时按此命名，不要另起一套。

```go
// Layer 上下文的一层。顺序即注入优先级。
type Layer int

const (
	LayerWorking   Layer = iota // 最近若干轮原文
	LayerEpisodic               // 滚动摘要
	LayerSemantic               // 结构化事实槽
	LayerRetrieval              // 按需召回的历史原文
)

// Scope 一层内容的归属范围。用于 I4：跨用户共享的对象不允许出现 User 级内容。
type Scope struct {
	UserID         string
	BotID          string
	ConversationID string
}

// Budget 一次组装的预算切分结果（单位：token）。
type Budget struct {
	Total     int
	Working   int
	Episodic  int
	Semantic  int
	Retrieval int
	Reserve   int // 未分配余量，用于吸收估算误差
}

// Fragment 注入模型的片段，带来源与保真等级。
type Fragment struct {
	Layer     Layer
	Role      string // system / user / assistant
	Content   string
	Tokens    int
	// Source 溯源：来自哪条消息、哪一轮。空表示合成内容（如摘要）。
	SourceMsgID string
	TurnIndex   int
	// Fidelity 保真等级：原样 / 改写 / 概括。用于注入时的措辞与校验。
	Fidelity Fidelity
}

type Fidelity int

const (
	FidelityExact      Fidelity = iota // 原文，逐字
	FidelityParaphrase                 // 改写，语义等价
	FidelitySummary                    // 概括，允许丢细节
)

// Assembly 一次组装的完整产物，便于回放与测试。
type Assembly struct {
	Messages    []Fragment
	Budget      Budget
	Omitted     []Omission
	Degraded    []Degradation
	TotalTokens int
}

// Omission 一条被丢弃的内容及其原因。
type Omission struct {
	Layer  Layer
	Reason string // budget / dedupe / invalid / conflict
	Count  int
}

// Degradation 一次降级事件。
type Degradation struct {
	Layer  Layer
	Reason string // timeout / parse_error / empty / disabled
}
```

### 5.1 为什么 Fragment 要带 Fidelity

注入时的措辞必须与保真等级匹配：

- `FidelityExact`：可以直接说"用户在第 3 轮提供了订单号 20261106001"。
- `FidelitySummary`：只能说"此前讨论过一个订单"，不能说"订单号是 X"。

把保真等级显式化，可以让"模型不知道这是概括还是原文"这个隐性错误变成类型错误。

---

## 6. 四层内存模型

### 6.1 为什么不是单池

单池预算的本质假设是：所有上下文片段的"价值/成本比"可比，所以按时间从新到旧装到装不下为止。

这个假设在两种输入下失效：

1. **价值与时间无关**。第 3 轮给的一个订单号，比第 80 轮的"嗯嗯"值钱得多。按时间装，前者必然先被挤掉。
2. **保真要求不同**。事实要求逐字，叙述允许概括。同一个截断策略对两者意味着完全不同程度的损失。

一旦承认这两点，单池就必然要做"分层 + 分策略"。四层不是设计美学，是这个结论的直接推论。

### 6.2 四层定义

| 层 | 内容 | 写入时机 | 保真 | 生命周期 |
|---|---|---|---|---|
| Working | 最近 N 轮原文 | 每轮追加 | Exact | 单次会话 |
| Episodic | 滚动摘要 | 溢出时增量合并 | Summary | 单次会话 |
| Semantic | 结构化事实槽 | 每轮抽取 + 冲突消解 | Exact | 单次会话 |
| Retrieval | 按需召回的原文片段 | 命中时构建 | Exact | 单次请求 |

注意：Semantic 与 `memory` 包里的 `UserMemory` 是**两个东西**，不要合并。

- `UserMemory`：跨会话、跨 Bot，描述"这个人是谁"（偏好、习惯、目标），TTL 以月计。
- `ConversationSlot`：单会话内，描述"这次对话里出现了什么"（订单号、金额、日期），TTL 与会话同寿。

把订单号写进 `UserMemory` 是错的：下个月他问"我那个订单"，模型会把三个月前的旧订单号当成当前订单。

### 6.3 预算推导

不再使用写死的 6000。改为从模型窗口推导：

```
W = 模型上下文窗口（token）
R = 输出预留（max(1024, 0.25 * W)）
S = system prompt + 工具 schema + 长期记忆（实测，用 estimateTokens 计算）
M = 安全余量 max(512, 0.05 * W)

H = W - R - S - M        // 本次可分配给历史的总额
```

代入几个真实窗口，单位 token：

| W | R | 假设 S | M | H |
|---|---|---|---|---|
| 8,000 | 2,048 | 1,200 | 512 | 4,240 |
| 32,000 | 8,000 | 2,400 | 1,600 | 20,000 |
| 128,000 | 32,000 | 3,200 | 6,400 | 86,400 |

`R = 0.25 * W` 的依据：ReAct 循环里模型要输出推理 + 可能的长答案，四分之一是经验下限；低于这个值会出现"上下文装满了、回答被截断"。

**H 必须由调用方注入，不能由 ctxmem 自己读配置。** 原因：同一个 Bot 可能切模型，同一个进程可能服务多个模型规格，配置是运行时的。

### 6.4 层间配额算法

固定比例（如 50/15/10/25）是错的：语义槽通常只需要 300 token，给 10% 是浪费；检索层大部分请求根本不需要，给它 25% 也是浪费。

正确做法是**保底 + 按需竞争 + 二次回收**：

```go
// 每层声明三件事：
//   Min：至少给多少（保证功能不退化）
//   Max：最多给多少（防止单层独占）
//   Demand()：本次实际需要多少
type LayerQuota struct {
	Min    int
	Max    int
	Demand func() int
}

func Allocate(h int, quotas [4]LayerQuota) Budget {
	need := [4]int{}
	minSum := 0
	for i, q := range quotas {
		need[i] = min(q.Demand(), q.Max)
		minSum += q.Min
	}

	// 保底总额超过 H：等比压缩保底，记一次 Degradation
	if minSum > h {
		return scaleDown(h, quotas)
	}

	var got [4]int
	assigned := 0
	for i, q := range quotas {
		got[i] = q.Min
		assigned += q.Min
	}

	// 剩余按 (need - Min) 权重分配，截到 Max；两轮回收未用额度
	rest := h - assigned
	for pass := 0; pass < 2 && rest > 0; pass++ {
		weight := 0
		for i := range quotas {
			weight += max(0, need[i]-got[i])
		}
		if weight == 0 {
			break
		}
		for i := range quotas {
			want := max(0, need[i]-got[i])
			give := min(rest*want/weight, quotas[i].Max-got[i])
			got[i] += give
			assigned += give
		}
		rest = h - assigned
	}

	// 所有层都到 Max 仍有余量：全部给 Working
	got[LayerWorking] += rest

	return Budget{
		Total:     h,
		Working:   got[LayerWorking],
		Episodic:  got[LayerEpisodic],
		Semantic:  got[LayerSemantic],
		Retrieval: got[LayerRetrieval],
	}
}
```

默认参数（可按 Bot 类型覆盖）：

| 层 | Min | Max | Demand 来源 |
|---|---|---|---|
| Working | 0.35H | 0.75H | 最近 N 轮实际 token |
| Episodic | 0.05H | 0.20H | 当前摘要 token |
| Semantic | 64 | 0.10H | 槽位格式化后的 token |
| Retrieval | 0 | 0.30H | 召回片段 token（未触发为 0） |

两轮分配是必要的：第一轮可能有层用不完（Semantic 只要 80 token，Min 给了 128），第二轮把释放出的额度分给够用的层。

最后那句"余量全给 Working"有依据：所有层都到上限还装不满，说明本次内容很少，此时多保留一条原文永远不亏。

### 6.5 层内装配顺序

装配顺序固定为 **Semantic → Episodic → Working → Retrieval**。理由：

1. Semantic 最小、最便宜、最重要，先占位——它只有几百 token，不该因为后面层超支而被挤掉。
2. Episodic 是单块，要么全进要么压缩，先定它的尺寸，剩余才好算。
3. Working 按轮次整块装载，装不下就停（不是跳过这一轮去装更早的，那会制造空洞）。
4. Retrieval 最后，它是"锦上添花"，任何情况下都可以被砍到 0。

**Working 层的装载单位是"轮"不是"条"。** 一轮 = 一条 user + 其后所有 assistant/tool 消息。按条装载会把 `assistant(tool_calls)` 和它的 `tool` 结果拆开，直接违反 I2。

### 6.6 溢出处理

| 层 | 溢出行为 | 理由 |
|---|---|---|
| Working | 停在这一轮之前，更早的轮次交给 Episodic | 原文可被摘要替代 |
| Episodic | 重新摘要压到 Max 以内；压不下再按段丢最早的 | 摘要可再压缩 |
| Semantic | 按优先级丢：manual > 冲突项 > 实体命中 > 最近更新 | 事实不能整块丢 |
| Retrieval | 按分数从低到高丢，丢到 0 为止 | 检索是尽力而为 |

---

## 7. 组装流水线

### 7.1 步骤

```go
func Assemble(ctx context.Context, in AssembleInput) (*Assembly, error) {
	// 1. 计算 H（调用方已给出窗口与 system 实测值）
	// 2. 并行拉取：摘要状态、槽位、候选历史
	// 3. 判定 Retrieval 是否触发，触发则检索（与 2 并行）
	// 4. Allocate 分配预算
	// 5. 按 §6.5 顺序装配
	// 6. 校验 I1/I2，失败则回退到"只保留 Working"
	// 7. 记录指标
	return asm, nil
}
```

第 2、3 步必须并行。串行会让一次请求多出一次 DB 往返加一次检索，在 50ms 级的对话链路里是可见的。

### 7.2 注入顺序

模型对上下文的注意力分布不均匀：首尾高、中间低（lost in the middle）。因此注入顺序按"重要性 × 位置敏感度"排：

```
[system]     人设 + 规则 + 长期记忆（per-user，每次重建，见 §12.3）
[user]       会话事实槽（Semantic，逐条列出，带来源）
[assistant]  确认收到背景
[user]       对话摘要（Episodic）
[assistant]  确认
[...]        Working 原文（按时间正序）
[...]        Retrieval 召回片段（带出处）
[user]       本轮问题
```

Retrieval 放在 Working 之后、当前问题之前，原因：它和当前问题语义最相关，紧邻能最大化注意力权重；放最前面会被淹没在历史里。

### 7.3 确定性

同一份输入必须产生同一份输出（I5）。这意味着：

- 所有 map 遍历必须先排序；
- 同分排序必须有全序键（分数 → 更新时间 → ID），现有 `selectMemories` 已经这么做了，照抄；
- 检索打分不能依赖浮点累加顺序；BM25 的 `idf` 先算好再乘，不要边遍历边累加；
- 时间只从 `ctx` 或注入的 `now()` 取，不直接调 `time.Now()`。

没有确定性，§15 的 golden test 全部无法写。

---

## 8. Semantic 层：结构化事实槽

### 8.1 抽取策略：规则优先，模型兜底

绝大多数关键事实是有形态的，能用正则抓，就不要用模型：

| 类型 | 形态 | 抽取方式 |
|---|---|---|
| `order_id` | 10–24 位数字/字母数字混合 | 正则 + 上下文词（订单/单号/编号） |
| `phone` | 11 位数字，1 开头 | 正则 |
| `email` | 标准邮箱 | 正则 |
| `amount` | 数字 + 元/块/¥/$ | 正则 + 单位归一 |
| `date` | 2026-11-06 / 11月6日 / 明天 | 正则 + 相对时间解析 |
| `url` | http(s):// 开头 | 正则 |
| `entity` | 人名/公司/产品 | 模型抽取（每 N 轮一次） |

规则优先的三个理由：

1. 零成本、零延迟、零幻觉；
2. 可单测（给定文本，给定槽位）；
3. 模型抽取的 `order_id` 会抄错一位，而且你无法发现。

模型只负责 `entity` 这一类没有固定形态的，且结果必须过 §8.4 的校验。

### 8.2 槽位模型

新增表 `conversation_slots`：

```go
type ConversationSlot struct {
	ID             string    `gorm:"primaryKey;size:36"`
	ConversationID string    `gorm:"index;size:36;not null"`
	Key            string    `gorm:"index;size:64;not null"` // order_id / amount / date ...
	Value          string    `gorm:"type:text;not null"`
	Kind           string    `gorm:"size:16"`                // id / number / date / amount / entity
	// SourceMsgID 该值首次出现的消息。I3 要求它非空且可查。
	SourceMsgID string `gorm:"size:36;not null"`
	TurnIndex   int
	// SupersededBy 被哪条槽位取代。非空表示这是历史值。
	SupersededBy string    `gorm:"size:36"`
	Confidence   float64   `gorm:"type:decimal(3,2)"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}
```

索引：`(conversation_id, key, created_at)`。查询永远是"这个会话的这个 key 的所有值，按时间倒序"。

### 8.3 冲突消解

同一会话、同一 key 出现两个不同值，只有三种合法处理：

1. **更正**：新值来源是 user，且证据消息更晚 → 新值成为 current，旧值 `SupersededBy` 指向新值。
2. **并存**：来源不同（一个来自 user，一个来自 tool 结果）且都未过期 → 两条都保留，注入时并列并标注来源。
3. **歧义**：同一轮里用户自己给了两个值（"订单 123，不对，是 124"）→ 两条都保留，注入时写"用户先说是 123，随后更正为 124"。

**禁止**按 confidence 高者胜。置信度是抽取置信度，不是事实正确性；用它裁决冲突等于用"我有多确定我听到了"决定"哪个是真的"。

### 8.4 校验（防幻觉）

任何进入 Semantic 层的值，必须满足：

1. `SourceMsgID` 指向的消息存在；
2. `Value` 是该消息内容的**子串**（ID/数字类要求逐字匹配）；
3. `TurnIndex` 与该消息的时间序一致。

第 2 条是关键：它把"模型编了一个订单号"变成"校验失败、丢弃、记指标"，而不是"用户看到一个不存在的订单"。

### 8.5 注入格式

Semantic 层注入为一段结构化文本，不使用 JSON（括号和引号在小预算下性价比低）：

```
【本次会话已确认的事实】
- 订单号：20261106001（第 3 轮，用户提供）
- 金额：¥1,280.00（第 12 轮，工具查询结果）
- 期望送达：2026-11-08（第 12 轮，用户提供）
- 注意：用户在第 40 轮曾把订单号说成 20261106002，已在第 41 轮更正为 20261106001。
```

最后那条"冲突提示"是必须的。只给最终值会让模型无法理解用户为什么记得另一个数，从而在用户再次提到旧值时表现得不一致。

### 8.6 每轮写入的代价控制

槽位抽取在消息落库后异步执行，不阻塞对话：

- 规则路：同步执行，微秒级，直接写库；
- 模型路：异步，每 5 轮或每累计 2000 字符触发一次，失败不影响任何东西。

规则路必须同步的原因：如果用户在下一轮立刻问"刚才那个订单号是多少"，异步还没写完就查不到。这条延迟窗口是用户可感知的。

---

## 9. Retrieval 层：会话内检索

### 9.1 触发条件

检索不是每轮都做（浪费预算和延迟），只在三种情况下触发：

| 编号 | 条件 | 例子 |
|---|---|---|
| T1 | 查询含指代词，且 Semantic 层有候选 | "我那个订单到哪了" |
| T2 | 查询含 ID 形态 token，但 Semantic 层没有这个值 | "查一下 20261106001" |
| T3 | 用户显式要求回溯 | "翻一下前面说的" / "我上次讲的方案" |

指代词表（可扩展，不要用模型判断）：

```
那个 / 这个 / 它 / 他 / 她 / 上次 / 之前 / 刚才 / 前面 / 那条 / 我说的
```

条件 T1 的"Semantic 有候选"很重要：如果槽位里根本没有订单号，"那个订单"检索什么都不知道，只能退化为 T2 式的关键词检索（用"订单"做 query），效果差但聊胜于无。

### 9.2 为什么不上向量

按重要性排序的三个理由：

1. **要的是精确匹配，不是语义相似**。"20261106001" 和 "20261106002" 的 embedding 距离极小，但它们是两个订单。向量检索在这里是负资产。
2. **语料太小**。单会话 10²–10³ 条消息，暴力扫描 + BM25 的延迟在毫秒级，向量化的构建成本反而更高。
3. **可解释性**。BM25 能说"因为命中了 订单/20261106001 两个词"，向量只能说"距离 0.83"，出问题时无法归因。

### 9.3 索引方案

中文不做分词（引入分词器就引入一个依赖和一个词典），用**字符二元组（bigram）**：

```
"订单号是20261106001" → 订单 / 单号 / 号是 / 是2 / 20 / 02 / 26 ...
```

倒排结构：

```go
type Index struct {
	mu    sync.RWMutex
	docs  map[string]*Doc     // msgID -> 文档
	terms map[string][]string // term -> []msgID
}

type Doc struct {
	MsgID     string
	TurnIndex int
	Role      string
	Content   string
	Len       int
}
```

构建时机：**按需 + 会话级 LRU**。第一次触发检索时，把该会话所有消息（含 60 条窗口外的）拉出来建索引，缓存 10 分钟或直到有新消息写入。不落库、不持久化——重建成本是 O(消息数)，微秒级。

会话消息数超过 5000 条时再考虑持久化倒排，那是另一个数量级的问题，现在不做。

### 9.4 召回与融合

两路召回，用 RRF 融合（复用 `internal/bot/tools/hybrid.go` 已有的思路）：

- 路 A：BM25(查询词, 全量消息)
- 路 B：槽位精确匹配（查询里的数字/ID 与槽位值逐字相等的消息，直接置顶）

```
score(d) = 1/(60 + rank_A(d)) + 2.0 * 1/(60 + rank_B(d))
```

K=60 的理由和混合检索那节一致：压低头部差距，避免单路极强时独占。这里路 B 是"精确命中"，本来就该赢，所以给 `w_B = 2.0`——否则一条恰好包含"订单"两个字的寒暄会把真正的订单消息挤下去。

召回 K=8，截断到预算后再取前 3。先 8 后 3 的原因：8 是给重排留余量，3 是给注意力留余量。

### 9.5 注入格式

每条召回必须带出处，且明确标注它是原文：

```
【历史原文片段】
[第 3 轮 · 用户] 订单号 20261106001，麻烦帮我查下物流。
[第 41 轮 · 用户] 刚才说错了，是 20261106001 不是 002。
```

不带出处的召回等于给模型一段来源不明的文本，它会当成"用户刚说的"，从而产生"您刚才提到"这种错误归因。

### 9.6 与摘要冲突时的优先级

顺序固定：**Semantic > Retrieval(原文) > Episodic(摘要)**。

理由：摘要是有损压缩的产物，原文是事实，槽位是从原文抽取并校验过的事实。三者冲突时，置信度依次递减。

实现上不写冲突检测算法，而是**注入时加一句裁决规则**（放在 system 末尾）：

```
当「已确认的事实」与「对话摘要」不一致时，以「已确认的事实」为准；
当「历史原文片段」与「对话摘要」不一致时，以原文为准。
```

用一行 prompt 解决排序问题，比写一套冲突消解算法便宜两个数量级。

### 9.7 失败与代价

| 情况 | 行为 |
|---|---|
| 索引构建超时（>200ms） | 放弃检索，记 Degradation，本轮不注入 |
| 无召回 | 不注入，不记错误（正常情况） |
| 召回全部低于阈值 | 不注入 |
| 预算不足 | 按分数从低到高丢，至少保留 1 条 |

检索永远不能成为请求失败的原因（I7）。

---

## 10. Episodic 层：摘要 v2

### 10.1 结构化契约

摘要从自由散文改成分段结构。让模型填槽，不让它写作文。

```json
{
  "background": "用户在处理一笔电商退款，先后确认过订单、金额与期望送达时间。",
  "facts": [
    {"key": "order_id", "value": "20261106001", "evidence": [3]},
    {"key": "amount", "value": "1280.00", "evidence": [12]}
  ],
  "decisions": ["选择退款而非换货"],
  "open_items": ["等待商家确认收货地址"],
  "rejected": ["上门取件（用户嫌时间不合适）"]
}
```

Prompt 要求（替换现有 `contextSummaryPrompt`）：

```
你是一个对话压缩器。把「已有摘要」与「新增对话」合并成新摘要，只输出 JSON。

规则：
1. background：不超过 200 字，只写叙事脉络，不写具体编号。
2. facts：把订单号、金额、日期、单号、地址等原样抄进 value，禁止改写、禁止四舍五入。
3. evidence：填写该事实出现的消息序号（对话中 [N] 标注的数字）。
4. decisions / open_items / rejected：各不超过 5 条，每条不超过 30 字。
5. 没有内容的字段输出空数组，不要省略字段。
6. 只输出 JSON，不要 markdown 代码块。
```

关键差异：`facts` 的 `value` 要求**原样抄写**，并带 `evidence` 供 §10.3 校验。散文体摘要做不到这一点，因为它没有"字段"这个概念。

### 10.2 增量合并

沿用现有的滚动模式，只改三处：

1. 合并输入带上上一版的结构化摘要（而不是纯文本），让模型知道哪些 facts 已经记录过；
2. `upToMsgID` 保持单调（I6），并发写用 CAS；
3. 合并后如果 `facts` 数量超过 50，按"最近更新 + evidence 数量"裁剪，而不是全量保留。

### 10.3 保真校验（必做）

摘要返回后，逐条校验 `facts`：

```go
for _, f := range summary.Facts {
    if !strings.Contains(原文拼接, f.Value) {
        drop(f)
        metrics.SummaryFactDropped.Inc()
        continue
    }
}
```

这一步把摘要从"信任模型"变成"可验证的压缩"。现有实现完全信任模型输出，这是 D7 之外更隐蔽的风险：模型可能把 1280 写成 1820，而没有任何环节能发现。

### 10.4 长度控制

- `background`：按 token 校验，超过 400 token 直接截断（rune-safe）；
- 整份摘要：超过 Episodic 层 Max 的 120% 时，重新摘要一次；仍超则按 `rejected → decisions → background` 的顺序丢弃，`facts` 最后才动。

顺序的理由：`rejected` 是"用户否掉的方案"，价值最低；`facts` 是最高的，任何情况下都不先丢。

### 10.5 摘要失败的处理

现有实现是"失败 → 返回空 → 溢出消息消失"。改为三级降级：

| 级别 | 条件 | 行为 |
|---|---|---|
| L1 | 解析失败但拿到了文本 | 用纯文本当 background，facts 为空 |
| L2 | 调用超时/报错 | 复用上一版摘要，溢出部分暂不合并（下一轮再试） |
| L3 | 连续 3 次失败 | 熔断 5 分钟，期间走 L2；同时告警 |

L2 是关键改进：现在失败即丢内容，改后失败只是"摘要停在旧版本"，原文仍在库里，下一轮可以补。这把一次性的数据损失变成了可重试的延迟。

### 10.6 摘要不能在请求关键路径上阻塞

现状：`BuildContextMessages` 内部同步调用 `chat()`，超时 30s。一次模型抖动就会让用户等 30 秒。

改为 **stale-while-revalidate**：

```
if 有旧摘要 && 旧摘要年龄 < 10min:
    直接用旧摘要，后台异步触发一次刷新
else:
    同步摘要，但超时 3s；超时则用旧摘要（可能为空）
```

这样正常路径下摘要永远不阻塞，最坏情况也只阻塞 3 秒。

---

## 11. 失败与降级矩阵

| 组件 | 失败表现 | 降级行为 | 用户可见影响 | 指标 |
|---|---|---|---|---|
| 历史拉取 | DB 超时 | 只用当前消息，无历史 | 模型不知道前情 | `ctx.db.fail` |
| 槽位查询 | DB 超时 | 跳过 Semantic 层 | 可能重复问用户信息 | `ctx.slot.fail` |
| 槽位抽取 | 正则/模型异常 | 该轮不写槽位 | 无 | `ctx.slot.extract_fail` |
| 摘要调用 | 超时/报错 | 用旧摘要 | 对早期对话记忆变淡 | `ctx.summary{result}` |
| 摘要解析 | JSON 非法 | 纯文本 fallback | 摘要质量下降 | `ctx.summary_parse.fail` |
| 摘要事实校验 | 子串不匹配 | 丢弃该条 fact | 该事实不可见 | `ctx.summary_fact.drop` |
| 索引构建 | 超时 | 跳过检索 | 无法回溯长历史 | `ctx.retrieval.skip` |
| 预算分配 | 保底总额超 H | 等比压缩，记录 | 每层都变薄 | `ctx.budget.squeeze` |
| 不变量校验 | I1/I2 失败 | 只保留 Working | 摘要与检索全部丢失 | `ctx.invariant.violation` |

最后一行是兜底：任何校验失败都退到最小可用集合（system + 最近若干轮原文 + 当前问题）。宁可"笨但正确"，不可"聪明但 400"。

---

## 12. 并发、缓存、存储

### 12.1 单飞（singleflight）

同一会话的摘要刷新必须单飞：否则 5 个并发请求会触发 5 次 LLM 摘要，成本乘以 5，且结果互相覆盖。

```go
var group singleflight.Group
res, err, _ := group.Do("summary:"+convID, func() (any, error) {
    return cc.summarize(ctx, convID, overflow)
})
```

注意：`singleflight` 的第一调用者的 `ctx` 会决定所有等待者的命运。若它被取消，其余请求也拿不到结果。因此摘要用的是**独立 background ctx + 超时**，不继承请求 ctx。

### 12.2 摘要状态存储

```go
type SummaryState struct {
	ConversationID string
	Version        int    // 结构版本，schema 变更时递增
	UpToMsgID      string // 已摘要到的消息，单调递增
	Payload        []byte // 序列化后的结构化摘要
	UpdatedAt      time.Time
}
```

| 选项 | 结论 |
|---|---|
| 进程内 map | 现状，不可用（D8） |
| Redis | **主选**。key `ctxmem:v2:{convID}`，TTL 30 天，写入用 Lua 做 CAS |
| 数据库 | 可选持久层。Redis 丢失时可从消息表重建，不强制 |

写入的 CAS 条件：

```lua
-- 仅当新的 upToMsgID 在会话内更晚时才覆盖
if redis.call('HGET', KEYS[1], 'up_to') < ARGV[1] then
    redis.call('HSET', KEYS[1], 'up_to', ARGV[1], 'payload', ARGV[2])
end
```

比较用消息的 `created_at` 毫秒时间戳 + ID 组合，避免纯 ID 比较（UUID 无序）。

### 12.3 长期记忆的位置（修 D1）

长期记忆**不能**进 `cfg.SystemPrompt`，因为 Agent 按 bot 缓存。

改为两种可行方案，二选一：

**方案 A（推荐）：记忆转成一条 system 之外的 user 消息**

```
[system]   人设 + 规则（per-bot，可缓存）
[user]     【关于该用户的已知信息】...（per-user，每轮拼接）
[assistant] 好的，我会参考这些信息。
[user]     会话事实槽 ...
```

优点：Agent 对象可安全共享；记忆每轮刷新；不需要改缓存结构。

**方案 B：Agent 缓存键加上 userID**

```go
cfg.ID = bot.ID + ":" + userID
```

缺点：缓存条目数从 O(bot) 变成 O(bot × user)，内存增长快，且失去了"同一 bot 共享模型连接/工具注册"的复用。

选 A。它把"per-bot 的配置"和"per-user 的运行时数据"在消息层面分开，而不是在缓存键层面打补丁。

### 12.4 长期记忆的注入预算

记忆当前是"最多 16 条，直接拼进 system"。改造后它不进 system，但要单独设预算：

```
memoryBudget = min(1200 token, 0.10 * W)
```

16 条中文记忆约 800–1500 token，1200 是合理上界。超过则按现有三因子分数裁到预算内，而不是硬截 16 条——条数和 token 不是一回事，8 条长记忆可能比 16 条短记忆更占地方。

### 12.5 并发安全

| 结构 | 保护方式 |
|---|---|
| 摘要状态 | Redis CAS（跨进程）+ singleflight（进程内） |
| 槽位写入 | DB 唯一索引 `(conversation_id, key, value)` 做幂等 |
| 倒排索引 | `sync.RWMutex`，读多写少，写时整体重建而不是增量修改 |
| 预算计算 | 纯函数，无状态 |

---

## 13. 观测指标

新增到 `pkg/obs/metrics.go`，命名与现有 `RecordLLMParse` 保持同风格。

| 指标 | 类型 | 标签 | 回答什么问题 |
|---|---|---|---|
| `ctx.assemble.duration` | Histogram | `bot_type` | 组装是否成为延迟瓶颈 |
| `ctx.tokens` | Histogram | `layer` | 预算是否按预期分配 |
| `ctx.omitted` | Counter | `layer`, `reason` | 丢了多少、为什么丢 |
| `ctx.degraded` | Counter | `layer`, `reason` | 哪一层在拖后腿 |
| `ctx.slot.hit` | Counter | `key` | 事实槽是否真的被用上 |
| `ctx.slot.conflict` | Counter | `key` | 冲突频率（高则说明抽取规则有问题） |
| `ctx.retrieval.trigger` | Counter | `rule`（T1/T2/T3） | 检索触发的分布 |
| `ctx.retrieval.recall` | Histogram | — | 召回条数分布 |
| `ctx.summary` | Counter | `result`（ok/timeout/parse_error） | 摘要健康度 |
| `ctx.summary_fact.drop` | Counter | `key` | 幻觉率（校验丢弃数/总数） |
| `ctx.invariant.violation` | Counter | `rule`（I1/I2） | 兜底触发次数，必须长期为 0 |

`ctx.summary_fact.drop` 是这套设计里最有价值的一个指标：它直接量化"摘要模型编造事实的频率"。有了它，"摘要可信"才是一个可证伪的陈述。

### 13.1 必须能回答的三个问题

1. 今天有多少比例的请求触发了降级？→ `ctx.degraded` 按 reason 聚合。
2. 长会话（>50 轮）的 token 都花在哪层？→ `ctx.tokens` 按 layer 聚合。
3. 结构化事实的召回率是多少？→ `ctx.slot.hit` / 会话总数。

答不上这三个，说明指标加错了。

---

## 14. 迁移与兼容

### 14.1 现有测试必须继续通过

| 文件 | 约束 |
|---|---|
| `context_compression_test.go` | 4 个用例，覆盖短历史/长历史/摘要失败/fetch 失败 |
| `context_budget_test.go` | 覆盖 rune 预算与孤儿 tool 修正 |
| `memory/*_test.go` | 9 个文件，覆盖抽取、合并、检索、写入 |

改造期间可以改测试的**断言**（因为行为变了），但不能删用例。每个被改的断言必须在 PR 描述里写清"为什么旧行为是错的"。

### 14.2 数据兼容

- 新增表 `conversation_slots`，`AutoMigrate` 自动建，不影响现有表；
- `summaryState` 的进程内结构改为 Redis，旧数据无法迁移（进程内本来就丢了），直接从空开始；
- 摘要格式从文本改为 JSON：`Version` 字段区分，`Version == 1` 的旧摘要按纯文本 `background` 处理，不阻塞。

### 14.3 灰度

```
第 1 周：P0 + P1，只修 bug + 统一预算口径，行为变化最小
第 2 周：P2 双写 —— 新链路照常走，同时离线计算"如果启用四层会怎样"，比对差异
第 3 周：P3 按 bot_id 哈希灰度 10%
第 4 周：全量
```

每个阶段都能独立回滚。P2 的双写模式是关键：它让你在用户无感的情况下拿到"新旧差异"的真实数据，而不是靠猜。

---

## 15. 测试计划

### 15.1 必测用例

| 编号 | 用例 | 断言 |
|---|---|---|
| T1 | 随机生成 1–200 轮对话，随机长度消息 | `Σ tokens ≤ H`，且不 panic |
| T2 | 同一份历史，分别用 8k / 32k / 128k 窗口 | 三者的 Working 层 token 单调递增 |
| T3 | 用户连发 3 条消息 | 3 条全部保留（回归 D2） |
| T4 | 中文 600 字消息 | 摘要输入无非法 UTF-8，截断点不在 rune 中间（回归 D3） |
| T5 | 用户 A 的 Bot 首次请求后，用户 B 请求同一 Bot | B 的消息里不含 A 的任何记忆（回归 D1） |
| T6 | 200 轮对话，第 3 轮含订单号 | 第 200 轮提问"那个订单"时，订单号出现在上下文中 |
| T7 | 第 3 轮给订单号，第 40 轮改口 | 注入内容同时包含两个值与更正说明 |
| T8 | 摘要模型返回编造的订单号 | 该 fact 被丢弃，`ctx.summary_fact.drop` +1 |
| T9 | 同一输入连续组装 100 次 | 100 次输出逐字节相同（回归 I5） |
| T10 | 摘要调用全部超时 | 请求仍成功，`Assembly.Degraded` 非空 |
| T11 | 检索索引构建超时 | 请求仍成功，不注入检索片段 |
| T12 | 构造一个必然降级的输入 | 指标能区分出是哪一层、什么原因 |
| T13 | Working 层装载后，第一条是否为 tool 消息 | 否（回归 I2） |
| T14 | 800 轮对话压测 | 单次组装 P99 < 20ms（不含摘要 LLM 调用） |
| T15 | 并发 50 个请求同一会话 | 摘要只被调用一次（singleflight） |

### 15.2 测试基建

必须先把 `estimateTokenCount` 抽成可注入接口，否则 T1 无法稳定复现：

```go
type TokenEstimator interface {
    Estimate(text string) int
    EstimateMessage(m *schema.Message) int
}
```

默认实现沿用现有算法（ASCII 0.25，非 ASCII 0.6，+50 固定开销），测试实现可以返回固定值，让预算边界可精确构造。

`EstimateMessage` 必须计入 `ToolCalls` 的函数名与参数（修 D6），并与 `messageRunes` 的口径统一。

---

## 16. 分阶段落地

### P0：修 S1 缺陷（半天）

| 项 | 改动 | 文件 |
|---|---|---|
| D2 | 同角色消息改为合并而非丢弃 | `context_compression.go:216` |
| D3 | 字节切片改 rune-safe 截断 | `context_compression.go:241`、`:233` |
| D4 | 分页拉取直到预算满足或达到硬上限（如 500 条） | `bot.go:1150`、`:1396` |
| D1 | 记忆移出 system prompt，改为 per-user 消息 | `bot.go:1788`、`agent/agent.go:492` |

验收：T3、T4、T5 通过。

### P1：统一预算口径 + 事实槽（1–2 天）

- 抽出 `TokenEstimator`，两层共用；
- `H` 改为按模型窗口推导，删除 6000 / 12000 两个常量；
- 新增 `conversation_slots` 表与规则抽取（同步路径）；
- Semantic 层注入。

验收：T2、T7、T9 通过；T1 的部分用例通过。

### P2：检索层（2–3 天）

- bigram 倒排 + BM25；
- 指代检测 + 三触发条件；
- RRF 融合 + 出处注入；
- 异步建索引 + LRU。

验收：T6、T11、T14 通过。

### P3：四层配额 + 观测（2 天）

- `Allocate` 保底/竞争/回收；
- 结构化摘要 + 事实校验；
- 全部指标接入。

验收：T8、T12、T13 通过；G3 可度量。

### P4：状态外置与多副本（1–2 天）

- Redis 摘要状态 + CAS；
- singleflight + 熔断；
- stale-while-revalidate。

验收：T10、T15 通过；重启后摘要不丢。

---

## 17. 验收标准

全部满足才算完成：

1. T1–T15 全部通过，且 T1 是 property test（随机 1000 次）。
2. `ctx.invariant.violation` 在 24h 灰度内为 0。
3. 长会话（>50 轮）的 `ctx.tokens` 分布中，Semantic + Retrieval 占比 > 0（否则说明这两层没生效）。
4. 关闭摘要模型（返回错误）时，请求成功率仍为 100%。
5. 同一会话在 3 个副本上分别组装，结果一致。
6. 订单号类事实的召回：T6 用例在 200 轮下稳定命中，且不依赖摘要。
7. 组装耗时（不含 LLM）P99 < 20ms。

---

## 18. 禁区

以下做法看起来能解决问题，实际会把系统带偏，明确禁止：

1. **上向量库**。见 §9.2。
2. **让摘要模型输出自然语言再正则解析**。要么约束成 JSON，要么不用它。
3. **把事实槽存进 `UserMemory`**。跨会话污染，见 §6.2。
4. **用 confidence 裁决冲突**。见 §8.3。
5. **在请求关键路径上同步调用摘要模型**。见 §10.6。
6. **按字节截断**。任何 `s[:n]` 都视为 bug。
7. **为每一层单独调一次模型**。四层不是四次 LLM 调用，只有 Episodic 需要模型。
8. **把 `upToMsgID` 用 UUID 字符串比较**。UUID 无序，必须用时间戳 + ID 组合。
9. **在 `cfg.SystemPrompt` 里放任何 per-user 数据**。见 D1。
10. **为了通过 T1 而把 `H` 调小**。预算的正确性靠推导，不靠保守。

---

## 19. 概念索引（给读代码的人）

这套设计涉及的概念，按"读到这里应该能解释清楚"整理：

| 概念 | 在哪一节 | 一句话解释 |
|---|---|---|
| Context engineering | §6.1 | 不是"塞更多上下文"，而是决定每一层该给多少、按什么顺序放 |
| Working / Episodic / Semantic memory | §6.2 | 分别对应"当前在聊什么 / 之前聊过什么 / 确定的事实是什么" |
| Lost in the middle | §7.2 | 模型对长上下文中间部分的注意力显著低于首尾 |
| Token budget derivation | §6.3 | 预算 = 窗口 − 输出预留 − system − 安全余量 |
| Min/Max/Demand 分配 | §6.4 | 先保底、再按需竞争、最后回收余量 |
| Bigram 倒排 | §9.3 | 中文不分词也能做检索：按相邻两字建索引 |
| BM25 | §9.4 | 词频 + 逆文档频率 + 长度归一，比 TF-IDF 更抗长文档 |
| RRF | §9.4 | 用排名而非分数融合多路召回，避免量纲不可比 |
| Singleflight | §12.1 | 同一 key 的并发请求只执行一次，其余等待结果 |
| CAS | §12.2 | 比较并交换，用单调游标保证并发写不倒退 |
| Stale-while-revalidate | §10.6 | 先返回旧值，后台刷新，用一致性换延迟 |
| 结构化摘要 | §10.1 | 让模型填字段而不是写作文，字段可以被校验 |
| 事实槽 | §8 | 把"确定的事实"从"叙述"里拆出来单独存储 |
| 保真等级 | §5.1 | 原样/改写/概括，决定注入时能不能说"用户说" |
| Prefix cache | §20.2 | 按 token 前缀复用 KV 计算，前缀一个字节变化就全部失效 |
| 约束解码 | §20.3 | 用语法约束把"生成合法 JSON"从概率问题变成确定问题 |
| Span 归因 | §20.4 | 每条记忆带原文区间，可校验、可回指、可精确撤销 |
| Contextual Retrieval | §20.5 | 索引前给 chunk 补一句定位上下文，解决块脱离文档不可读 |
| Late chunking | §20.6 | 先对全文嵌入再按块池化，让块向量携带全文语境 |
| Cross-encoder | §20.7 | 把 query 和 doc 拼起来打分，用于 RRF 之后的精排 |
| Compaction | §20.8 | 类比 LSM：离线合并、去重、消解冲突，不占请求路径 |
| RAPTOR | §20.9 | 递归聚类摘要成树，检索可跨粒度 |

---

## 20. 前沿增强（Advanced Track）

### 20.0 先定规矩

这一章回答一个具体问题：**想讲出别人没有的东西，应该往哪里加？**

结论先说：**不改 §5–§12 的核心结构。** 四层内存、Min/Max/Demand 配额、结构化事实槽、bigram 检索是地基，动了就要重写全部测试。这一章全部是**可插拔的增强层**：每个增强有独立开关、独立指标、独立回滚路径，关掉之后系统仍然正确，只是少了一项能力。

判断一个增强值不值得做，只问三句话：

1. **能不能被指标证明？** 说不清度量方式的，是故事不是工程。
2. **失败模式是什么？** 答不出来的，说明还没想清楚。
3. **拿掉它系统还正确吗？** 拿掉就错的，它不是增强，是地基，应该进 §4 的不变量。

按这三条分档：

| 档 | 含义 | 处理 |
|---|---|---|
| 现在做 | 成本 < 1 天，收益立刻可见，或能补现有缺陷 | 进 P1–P4 |
| 有余力做 | 成本 1–3 天，需要 A/B 才能证明 | 主干完成后按指标挑 |
| 知道但不做 | 能在面试里讲清取舍，但不进代码 | 写进 §20.12 |

**什么时候才值得大改？** 只有当"规模或语义"发生了质变：单机内存装不下、跨会话记忆要服务百万用户、需要多租户隔离、需要在线学习。那时候换的不是某个函数，是存储模型和一致性模型。现在这套的负载是"单会话几百到几千条消息"，用四层内存 + Redis 状态是匹配的；为了"用上新技术"去换向量库或图数据库，是把复杂度花在没有收益的地方。

### 20.1 总览

| # | 能力 | 解决什么 | 成本 | 主要风险 | 档位 |
|---|---|---|---|---|---|
| A1 | 前缀稳定性 / KV Cache 亲和 | 长上下文重复计费、TTFT 高 | 1 天 | 为命中牺牲指令优先级 | 现在做 |
| A2 | 约束解码 | 结构化输出解析失败 | 0.5 天 | 约束过强导致字段退化 | 现在做 |
| A3 | Span 级归因 | 记忆不可回指、改口无法失效 | 1 天 | 中文归一化漏判 | 现在做 |
| B1 | Contextual Retrieval | chunk 脱离文档不可读 | 1.5 天 | 前缀污染 BM25 | 有余力 |
| B2 | Late Chunking | 块间指代丢失 | 1 天 | 需换 embedding 模型 | 有余力 |
| B3 | Cross-encoder 精排 | RRF 之后仍不相关 | 0.5 天 | 领域不匹配 | 现在做 |
| C1 | 离线记忆整合 | 记忆膨胀、冲突堆积 | 2 天 | 覆盖用户新事实 | 有余力 |
| C2 | 层次摘要（RAPTOR 式） | 超长会话检索粒度单一 | 2–3 天 | 父摘要污染子节点 | 有余力 |
| D1 | 投机预取 | 检索增加首字延迟 | 1 天 | 成本放大 | 有余力 |
| D2 | Context Manifest | 装配过程不可解释 | 0.5 天 | 隐私 | 现在做 |

---

### 20.2 A1 前缀稳定性：把 KV Cache 当成一等公民

**卖点**：上下文层不只是"决定放什么"，还决定了推理侧**能不能复用上一次的计算**。同一个 token 预算，排布不同，成本可能差 5–10 倍。

#### 机制

主流推理侧的 prefix cache（vLLM 的 Automatic Prefix Caching、SGLang 的 RadixAttention、以及各家 API 的上下文缓存计费）本质是同一件事：

> 以 **token 序列的前缀**为单位缓存 KV。前缀完全一致 → 直接复用；有一个 token 不同 → 该位置之后全部失效。

三个直接推论：

1. 缓存粒度是**块**（常见 16 token），不是消息。
2. 前缀匹配是**严格按位置**的，不是"内容相似"。
3. 越靠前的内容越值钱：改第一个 token，后面全废；改最后一个 token，只废最后一块。

这跟语义缓存是两件事，必须分清：

| | 语义缓存（已有） | 前缀缓存（本节） |
|---|---|---|
| 缓存对象 | **答案/结果** | **KV 计算** |
| 命中条件 | 语义相似度 ≥ 阈值 | 前缀 token 完全一致 |
| 命中后 | 跳过 LLM 调用 | 仍然调用，但跳过 prefill |
| 失效原因 | 知识更新、阈值漂移 | 前缀任何一个字节变化 |
| 收益 | 省一次完整调用 | 省 prefill 时间与计费 |

#### 真正的设计冲突

§6.5 写的装配顺序是 **Semantic → Episodic → Working → Retrieval**，理由是"小的、重要的先占位，防止被后面挤掉"。

这是**配额顺序**，不是**排布顺序**。两者被混为一谈，是大多数上下文系统错过缓存的原因。

- 配额顺序解决"给多少"：先算账，避免 Semantic 被 Working 挤没。
- 排布顺序解决"放哪里"：决定前缀稳定性和模型注意力。

**先算预算，再排版。** 配额一旦算定，排布不会改变任何一层拿到的 token 数。所以两者可以完全解耦：

```go
// Allocate 决定"给多少"（§6.4，不变）
// Layout  决定"放哪里"（本节新增，只影响顺序，不影响大小）
func Layout(a Assembly, q Budget) []Message {
	// 稳定性降序，同时满足两条硬约束：
	//   1. 指令在最前（system 优先级最高）
	//   2. 最新一条 user 在最后（recency，模型对末尾最敏感）
	return concat(
		a.StaticPrefix,   // system + 工具 schema + 固定策略：几乎不变
		a.Episodic,       // 摘要：每 K 轮才变一次
		a.Semantic,       // 事实槽：事实变化时才变
		a.Working[:last], // 历史轮次：追加式，不删就不变
		a.Retrieval,      // 检索片段：每条 query 都变
		a.Working[last],  // 最新 user：永远在最后
	)
}
```

这解决了 §6.5 里"Semantic 必须最先"和"缓存要求稳定内容在前"的矛盾：Semantic 的**配额**仍然优先，但它不必排在**物理最前**。Episodic 比 Semantic 稳定（摘要按块更新，事实槽随时可能新增），所以 Episodic 排在 Semantic 前面。

#### 稳定性契约

进入 `StaticPrefix` 的内容必须通过 `Canonical()`，否则一个字节就能让整段前缀失效：

```go
// 任何进入前缀的内容都必须可规范化。
// 违反以下任意一条，视为 bug（进 §4 不变量 I8）：
//   - 含时间戳、request_id、随机数、trace_id
//   - 含 map 的无序迭代结果
//   - 含未排序的切片（工具列表、能力列表）
//   - 数字格式不统一（"1" 与 "1.0" 与 "1.00"）
//   - 含 per-user 数据（同时违反 D1 与租户隔离）
func (f Fragment) Canonical() string

func PrefixFingerprint(parts []Fragment) string {
	h := sha256.New()
	for _, p := range parts {
		if !p.InPrefix {
			continue
		}
		io.WriteString(h, p.Canonical())
		h.Write([]byte{0}) // 分隔符，防止 "ab"+"c" 与 "a"+"bc" 撞车
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
```

指纹有三个用途：日志里能直接看出"这次为什么没命中"；灰度时能对比两个版本的稳定性；线上可以按指纹聚合命中率，而不是只看全局平均。

#### 摘要频率与命中率是一对矛盾

这是本节最容易被忽略的一点：

| 摘要策略 | 前缀变化频率 | 前缀命中率 | 代价 |
|---|---|---|---|
| 每轮重摘要 | 每轮 | ≈ 0 | 摘要最新，但缓存全废 |
| 每 4 轮 | 25% 的轮次 | ≈ 75% | 折中 |
| 每 8 轮 | 12.5% 的轮次 | ≈ 87.5% | 摘要最多滞后 7 轮 |
| 只在溢出时摘要 | 不确定 | 不可预测 | 命中率抖动，难做容量规划 |

建议 **K = 8，块边界触发**，并且只在"本轮确实会溢出"时才提前触发一次。理由：定时触发会在不需要的时候改动前缀；纯溢出触发会让前缀变化不可预测，容量规划做不了。块边界 + 溢出兜底，既有上界又可预测。

#### 指标

| 指标 | 定义 | 期望 |
|---|---|---|
| `ctx.prefix.stable_ratio` | 相邻两次请求的 prefix 指纹相同比例 | > 80% |
| `llm.cache_read_tokens / llm.input_tokens` | 推理侧报告的缓存命中占比 | 长会话 > 50% |
| `llm.ttft.p50 / p99` | 首 token 延迟 | 开启后 p50 下降可测 |
| `ctx.layout.segment_tokens` | 每段实际 token | 用于定位"谁把前缀撑爆" |

#### 失败模式

1. **为了命中把安全策略挪到后面。** 指令优先级下降，模型开始忽略约束。安全类指令永远在最前，宁可牺牲命中率。
2. **把 per-user 数据塞进 system。** 不只是 D1 的隐私问题，还让**每个用户的前缀都不同**，全站命中率归零。修 D1 是双赢。
3. **工具 schema 顺序不稳定。** Go map 遍历顺序随机，序列化后前缀每次都不同。必须排序。
4. **自建推理集群的租户隔离。** 前缀缓存按 token 块共享，若多租户共用一个 engine，相同前缀会命中同一份 KV。计算复用本身没问题，但要注意 timing 侧信道与租户边界；商用 API 由 provider 负责，自建时要显式隔离。
5. **把"命中率"当成越高越好。** 前缀命中率高但回答质量下降，说明你把该变的内容也冻结了。命中率必须与 §20.13 的质量指标一起看。

#### 面试讲法

> "上下文装配我做了两阶段：先按 Min/Max/Demand 算预算，再按稳定性排布。因为 prefix cache 是按 token 前缀严格匹配的，排布顺序直接决定推理成本。我把进入前缀的内容做了一层规范化契约，时间戳、map 迭代、per-user 数据都不许进，指纹进日志。这样长会话的 cache read 占比能过半，TTFT 明显下降。"

追问预案：

- "命中率多少？" → 答设计目标 + 实测口径，不要编数。
- "为什么不直接把历史全放前面？" → 历史是追加的，但窗口滑动会破坏前缀；所以摘要按块更新，K=8。
- "和语义缓存什么关系？" → 一个缓存结果，一个缓存计算，互补不替代。

---

### 20.3 A2 约束解码：把"解析失败"变成"不会失败"

**卖点**：系统里已经有 `pkg/obs/metrics.go:539` 的 `RecordLLMParse(ctx, module, result)`，说明解析失败是被观测的。有观测就说明有失败率，有失败率就值得从"重试"升级为"不会失败"。

#### 三级方案

| 级别 | 做法 | 适用 | 失败率 |
|---|---|---|---|
| L1 | grammar / JSON Schema 约束解码（XGrammar、Outlines、GBNF、`json_schema` strict） | 自建推理或支持的 provider | ≈ 0（语法层不可能出错） |
| L2 | JSON mode + schema 校验 + 一次修复重试 | 只支持 JSON mode 的 provider | 低 |
| L3 | 自由文本 + 正则 + 重试 | 兜底 | 中 |

关键是**能力探测 + 显式降级**，而不是假设 provider 支持：

```go
type StructuredDecoder interface {
	// 返回是否真的施加了语法约束。
	// 不允许"假装支持"：provider 忽略 schema 时必须返回 false，
	// 由上层决定降级到 L2 还是直接失败。
	Decode(ctx context.Context, schema Schema, prompt string) (json.RawMessage, bool, error)
}
```

#### Schema 设计规则

约束解码不是"把 schema 丢给模型"就完事，schema 本身会改变模型行为：

1. **全部字段 required。** optional 字段在约束解码下经常被填成空串或默认值，比缺失更难查。
2. **枚举优先于自由文本。** `type: "fact" | "preference" | "event"` 比 `description: "事实类型"` 稳定一个数量级。
3. **禁止深层 oneOf/anyOf。** 语法复杂度上升会让模型在结构里"绕路"，输出合法但语义错。
4. **每个字符串字段给 `maxLength`。** 否则模型会用超长文本填满，直接撑爆下游预算。
5. **带 `schema_version`。** 解析结果入库时必须能追溯是哪版契约产生的。

#### 失败模式

1. **约束过强导致语义退化。** 模型为了满足语法，把不确定的字段填成默认值。对策：允许 `"unknown"` 作为显式枚举值，并在下游当作缺失处理。
2. **provider 静默忽略 schema。** 返回 200 但输出自由文本。对策：`Decode` 必须回传"是否真的约束"，并在指标里区分。
3. **修复重试放大延迟。** 一次失败 + 一次重试 = 双倍 token。对策：`repair_rate` 超过 5% 就告警，说明 schema 或 prompt 有问题，不是靠重试解决。
4. **中文与转义。** JSON 里的中文引号、换行、控制字符是高频失败点；约束解码能消除语法错误，但消除不了语义错误。

#### 指标

`llm.parse.fail_rate{module,schema_version}`、`llm.parse.repair_rate`、`llm.parse.constrained{true|false}`。目标：约束可用时 fail_rate < 0.1%，repair_rate < 1%。

#### 面试讲法

> "结构化输出我不靠 prompt 里写'请返回 JSON'，而是做能力探测 + 约束解码，并且让 decoder 显式告诉我这次到底有没有真的施加语法约束。因为 provider 静默忽略 schema 是最坑的失败模式——你以为成功了，其实拿到的是自由文本。"

---

### 20.4 A3 Span 级归因：让记忆可回指

**卖点**：现在的事实槽是"模型抽取的结果"，面试官问一句"模型抽错了怎么办"就答不上来。加上归因之后，每条记忆都能指回原文的某一段，这是从"相信模型"到"可验证"的分界线。

#### 数据结构

```go
type Slot struct {
	Key      string // "order_id" / "budget" / "address"
	Value    string
	MsgID    string // 来源消息
	Start    int    // 原文 rune 偏移，左闭
	End      int    // 右开
	Quote    string // 原文片段，必须逐字保存，不能存模型改写后的值
	QuoteSHA string // 归一化后的哈希，用于快速比对
	Version  int64  // 单调递增，冲突消解用
}
```

**必须存 `Quote` 原文，而不是"模型认为的值"。** 模型可能把"五千左右"写成"5000"，这时 `Value` 可以是规范化后的"5000"，但 `Quote` 必须是"五千左右"，否则归因就是假的。

#### 三级校验

| 级别 | 检查 | 不通过时 |
|---|---|---|
| V1 存在性 | `MsgID` 存在且未被撤回 | 丢弃槽 |
| V2 字面 | 归一化后 `Quote` 是消息内容的子串 | 丢弃槽 |
| V3 一致 | `Value` 与 `Quote` 语义一致（数字/日期必须精确匹配） | 标记 `unverified`，不注入 |

中文归一化要处理的：全角/半角、零宽字符、连续空白、中英文标点、数字的千分位与中文数字。**数字、订单号、日期、金额这四类必须走精确匹配，不能容忍编辑距离。**

#### 连带收益：改口与撤回

有了 `MsgID`，用户可以"撤回/编辑"一条消息，系统按 `source_msg_id` 精确失效所有派生槽，而不是靠语义相似度猜。这是很多记忆系统做不到的——它们只存摘要，摘要一旦生成就无法精确撤销。

#### 失败模式

1. **模型改写原文。** 对策：抽取 prompt 要求同时输出 `quote`，并强制 `quote` 是原文子串；不满足就重试一次，仍不满足则标 `unverified`。
2. **偏移量按字节还是 rune。** Go 的 `len(string)` 是字节；中文一个字 3 字节。全链路统一 rune，落库前转换，禁止混用。
3. **消息被截断后偏移失效。** 若原文超过单条上限被截断，`End` 必须落在截断后的文本上，并在槽上标记 `truncated`。
4. **注入时泄露内部 ID。** 给模型看的引用标记用短号（`[U12]`），不要把 UUID 直接塞进 prompt，既浪费 token 又容易被复述出来。

#### 面试讲法

> "我的记忆不是模型总结出来的一段话，是带原文 span 的结构化槽。每条槽有来源消息、字符区间和原文引用，注入前做三级校验：消息还在、引用是子串、值和引用语义一致。数字类字段必须精确匹配，不接受模糊。这样用户撤回一条消息，我能精确失效所有派生记忆，而不是靠相似度猜。"

---

### 20.5 B1 Contextual Retrieval：给 chunk 补上下文再索引

**卖点**：这是目前"检索质量"里性价比最高的公开方法，而且和已有的 BM25 + 向量 + RRF 完全兼容，不需要换架构。

#### 问题

切块之后，块会失去它所在的语境：

> 原文："退款到账时间取决于支付渠道。微信零钱实时到账；银行卡 T+1；信用卡 T+3。"
> 切出的块："银行卡 T+1。"

用户问"信用卡退款多久"，这个块单独看没有"退款"两个字，BM25 召不回，向量也偏。**块级检索的失败，很大一部分不是排序问题，是块本身不可读。**

#### 机制

索引时为每个块生成 50–100 token 的"定位上下文"，拼在块前面再建索引：

```
[文档：退款政策 | 章节：到账时间 | 上文：不同支付渠道的退款时效]
银行卡 T+1。
```

Anthropic 公开的 Contextual Retrieval 报告里，contextual embeddings 让 top-20 检索失败率下降约 49%，加上 BM25 约 67%，再加 rerank 约 71%。**这些是他们在自家语料上的数字，不能当成我们的结果引用。** 我们能承诺的只有：同一套 golden set 上做 A/B，报告自己的 delta。

#### 成本工程：和 A1 联动

朴素做法是每个块调一次 LLM，索引成本爆炸。正确做法是用**前缀缓存**：

```
[缓存前缀：整篇文档 + 生成指令]  ← 同一文档的所有块共用，只算一次 prefill
[可变尾部：这是第 N 个块：<chunk>] ← 每个块不同
```

这正是 Anthropic 那套 recipe 的关键：没有 prompt caching，这个方法在经济上不成立。所以 A1 不是"锦上添花"，它是 B1 的前置条件。

#### 落地要点

1. 索引期离线任务，不进查询路径。
2. `context_prefix` 单独存一列，不要覆盖原 chunk，便于回滚与 A/B。
3. 增量索引：文档更新时只重算变化的块。
4. 前缀长度硬上限 120 token，超了截断——前缀是"定位"不是"复述"。

#### 失败模式

1. **前缀污染 BM25。** 前缀里反复出现的"文档/章节/退款"会抬高所有块的词频，稀释真正的关键词。对策：BM25 只索引原 chunk，向量索引 `prefix + chunk`，两路各取所长；或者对前缀词做降权。这一点必须做 A/B，不能想当然。
2. **前缀由模型生成，可能编造。** 对策：前缀只允许引用文档标题、章节标题、前后各一句原文，禁止自由发挥；生成后做子串校验。
3. **索引成本被低估。** 一万个块 × 100 token 输出，即使有缓存也是一笔钱。先在小语料上算清楚单位成本。

#### 面试讲法

> "我的混合检索里 BM25 和向量都建立在 chunk 上，但 chunk 切完会丢语境——'银行卡 T+1' 这个块里没有'退款'两个字。所以我做了 Contextual Retrieval：索引时给每个块生成一句定位上下文，再拼回去建索引。关键是成本：整篇文档放缓存前缀，只有块在变，否则每个块一次 LLM 调用在经济上不成立。"

---

### 20.6 B2 Late Chunking：先嵌入，再切块

**卖点**：和 B1 解决同一个问题，但走的是另一条路——不改索引文本，改嵌入时机。两条路可以 A/B，面试时能讲出"我评估过两种方案"。

#### 机制

常规做法：先切块 → 每块单独嵌入。块与块之间的指代在嵌入时已经丢了。

Late chunking：先对**整篇文档**做长上下文嵌入，得到 token 级向量；再按块边界做池化（mean pooling），得到块向量。这样每个块向量都"看过"全文。

#### 对比

| | Contextual Retrieval（B1） | Late Chunking（B2） |
|---|---|---|
| 改什么 | 索引文本 | 嵌入时机 |
| 需要 LLM | 是（可用缓存摊销） | 否 |
| 需要长上下文 embedding | 否 | 是（token 级输出） |
| BM25 是否受益 | 是（若前缀进 BM25） | 否 |
| 存储 | 多存一份前缀 | 向量维度不变，但要能重算 |
| 主要风险 | 前缀污染、生成幻觉 | 模型不支持、长文档显存 |

#### 决策

默认先做 B1：不换模型、BM25 也能受益、可解释。B2 作为备选，只在"块间指代极多且 BM25 贡献低"的语料上做 A/B。判断依据看现有指标：如果向量路单独 recall@20 明显低于 BM25，说明语料更依赖关键词，B1 更合适；反之考虑 B2。

#### 失败模式

1. 模型声称支持长上下文，但只返回一个 pooled 向量，拿不到 token 级输出——做不了。
2. 文档超长被截断，late chunking 退化成普通切块，且没人发现。必须校验输入长度。
3. 池化方式（mean vs cls）与模型训练方式不匹配，效果反而下降。按模型卡推荐来。

---

### 20.7 B3 Cross-encoder 精排：RRF 之后还有一步

**卖点**：RRF 解决的是"两路分数不可比"，不是"哪个更相关"。这两件事经常被混为一谈，能分清就是加分项。

#### 管线

```
BM25 top50 ─┐
            ├─ RRF ─→ top30 ─→ cross-encoder ─→ top8 ─→ 上下文
向量  top50 ─┘
```

RRF 只看排名，天然丢失"这一条到底有多相关"；cross-encoder 把 query 和 doc 拼在一起过一遍模型，直接输出相关性分数，精度高但只能对小候选集做。

#### 指标

| 指标 | 说明 |
|---|---|
| `recall@20` | 召回阶段的上限，rerank 不能提升它 |
| `nDCG@10` | 排序质量，rerank 的主指标 |
| `MRR` | 第一个正确结果的位置 |

**先看 recall@20。** 如果召回阶段就漏了，rerank 再好也没用。这也是为什么 B1/B2 和 B3 不冲突：一个提召回，一个提排序。

#### 成本

cross-encoder 一次 forward 的成本比生成低两个数量级，30 条候选完全可接受。真正要控制的是**候选集大小**和**输入长度**（chunk 超过模型上限要截断，截断策略会影响分数）。

#### 失败模式

1. 领域不匹配：通用 reranker 在代码/医疗语料上可能不如 BM25。必须用 golden set 验证，不能默认"rerank 一定更好"。
2. 分数不可比：不同 batch 的分数不要直接比，只用于同一次 query 内部排序。
3. 延迟：rerank 是同步调用，P99 要单独观测，必要时降级为"只对 top10 做"。

#### 面试讲法

> "RRF 解决的是多路分数量纲不可比，它只看排名，所以会丢掉'这条到底多相关'。后面我接了一层 cross-encoder 精排，top30 进 top8。评估的时候先看 recall@20 再看 nDCG@10——召回漏了，精排是救不回来的。"

---

### 20.8 C1 离线记忆整合：把 compaction 搬进记忆系统

**卖点**：这是系统设计层面的类比，面试官会明显感到你读过存储系统，而不是只会调 API。

#### 三种消解时机

| 时机 | 代表 | 优点 | 缺点 |
|---|---|---|---|
| 写时消解 | Mem0 的 ADD/UPDATE/DELETE/NOOP | 读路径干净、无重复 | 写延迟高；冲突判定缺上下文 |
| 读时消解 | Logos 现状（三因子打分） | 写快、实现简单 | 重复堆积；每次都要算 |
| 离线消解 | 本节 | 不占请求路径；可用更多上下文判冲突 | 有延迟；需要幂等与回滚 |

Logos 选"读时 + 离线"是合理的：聊天写入对延迟敏感，而"这两条记忆是不是同一件事"往往需要看更多历史才能判断，放在写路径上只能拍脑袋。

#### 类比 LSM

```
memtable（本轮新记忆，读时直接可见）
   ↓ 达到阈值 flush
L0（近期记忆，可能有重复/冲突）
   ↓ compaction：去重、合并、提升置信度
L1（稳定记忆，已消解）
   ↓ 长期不用
归档（可检索但不注入）
```

"compaction" 这个词能直接把记忆系统和存储系统连起来：写放大、读放大、空间放大三者的权衡，在这里同样成立。

#### 任务设计

```go
type CompactJob struct {
	UserID    string
	Watermark int64 // 只处理 version <= watermark 的记忆，避免与在线写冲突
	Batch     int   // 单批上限，默认 200
}

// 幂等：同一 watermark 重复执行结果一致。
// 可回滚：不物理删除，写 superseded_by 链。
// 可观测：每次产出 compacted / merged / archived 三个计数。
func (j *CompactJob) Run(ctx context.Context) (Report, error)
```

三条硬约束：

1. **水位线**。任务开始时记下最大 version，只处理这之前的。在线写入不受影响。
2. **不物理删除**。冲突的旧记忆标 `superseded_by`，保留审计链；用户改口时能解释"为什么现在这个是对的"。
3. **CAS 提交**。合并结果写回时比对 version，变了就放弃这一批，下轮重来。

#### 衰减不要用硬 TTL

硬 TTL 会把"每年只用一次但很重要"的记忆（比如身份证号）删掉。用 `score = confidence × recency × (1 + log(1+access_count))`，只做**排序降权**，不做删除；只有超过很长窗口（如 365 天）且从未被召回，才归档。

#### 失败模式

1. **离线任务把用户刚改的事实覆盖回去。** 水位线 + CAS 必须同时有。
2. **合并把两条不同事实揉成一条。** 只允许"同 key 同实体"合并；不确定时保留两条，标 `conflict`，交给读时按 §8.3 处理。
3. **任务雪崩。** 全量扫描 + 无批上限，一次跑挂数据库。必须有 batch、限速、可中断。

#### 面试讲法

> "记忆系统我类比 LSM：写入先进 memtable，读时可见；到阈值后离线 compaction，做去重、冲突消解和置信度提升。关键是 compaction 不能覆盖在线写，所以我用 version 水位线加 CAS，冲突的旧记录不物理删除，留 superseded_by 链。这样用户改口时我能解释清楚为什么现在这条是对的。"

---

### 20.9 C2 层次摘要：RAPTOR 式多粒度

**卖点**：单层摘要只能回答"这段聊了什么"，回答不了"这个月我们主要在解决什么"。层次摘要是把"粒度"变成可检索的维度。

#### 结构

```
主题层（每 10 个会话聚一次）
   ├── 会话层（每 20 轮）
   │      ├── 段层（每 5 轮）
   │      └── ...
   └── ...
```

检索时三层都参与，用层级权重做融合：段层权重高（具体），会话层中，主题层低但用于"跨会话指代"。

#### 什么时候值得

不要默认做。判据：会话长度分布 **P95 > 150 轮**，且用户有明显的"跨天继续同一件事"行为。否则层次树的构建成本（O(n log n) 次 LLM 调用）换不来收益。

#### 失败模式

1. **父摘要污染子节点检索。** 父摘要天然更泛，容易在所有 query 上都有中等相似度。对策：层内分数归一化后再融合，父层给固定折扣（如 0.7），并单独观测父层命中率。
2. **摘要树与原文不一致。** 每层摘要都要过 §10.3 的保真校验，不能因为"是上层"就放松。
3. **重建成本。** 会话更新后，从段到主题要逐层失效。用内容哈希做节点 key，未变化的子树直接复用。

---

### 20.10 D1 投机预取：用统计换延迟

**卖点**：这是"产品感"的体现——不是所有优化都要改架构，有时候提前 300ms 做对的事就够了。

#### 触发时机

| 时机 | 说明 |
|---|---|
| 输入停顿 300ms | 用户大概率在想下一句，但不一定发 |
| 流式输出中 | 模型还在说，用户下一步可能追问 |
| 工具调用等待 | 有几百毫秒的空窗，正好用来预取 |

#### 预测信号

不训练模型，只用现成的：会话主题（Episodic 的 topic）、最近提到的实体（Semantic 槽）、上一条 user 的 bigram。三个信号各出一个候选 query，最多预取 1 条。

#### 三个护栏（缺一不可）

1. **可取消**：`context.WithCancel`，用户一输入就取消，别让预取占着连接。
2. **预算**：每轮最多 1 次，token 计入 §13 的成本指标。
3. **自适应**：滚动窗口命中率 < 20% 自动关闭，> 35% 再打开。防止"为了优化而优化"。

#### 指标

`prefetch.hit_rate`、`prefetch.wasted_tokens`、`prefetch.ttft_delta`。如果 TTFT 没降、wasted_tokens 涨了，就是负收益，直接关掉。

#### 失败模式

1. 预取结果过期：用户在预取后改了条件。对策：预取结果带 query 指纹，使用时比对。
2. 预取和真实请求抢配额：预取必须走低优先级队列，可被真实请求抢占。
3. 隐私：预取会把"用户可能问但没问"的内容送去检索，日志里不要记录预取 query 原文。

---

### 20.11 D2 Context Manifest：让装配可回放

**卖点**：前面所有优化，如果没有一个"这次到底装了什么"的记录，线上出问题只能靠猜。Manifest 是让整个上下文层**可解释**的基础设施。

#### 结构

```json
{
  "trace_id": "…",
  "budget": {"total": 6000, "working": 3400, "episodic": 900, "semantic": 120, "retrieval": 0},
  "segments": [
    {"layer": "working", "tokens": 3380, "turns": 7, "dropped": 2, "reason": "budget"},
    {"layer": "semantic", "tokens": 118, "slots": 6, "unverified": 1}
  ],
  "prefix_hash": "a1b2c3d4",
  "retrieval": {"triggered": false, "reason": "no_anaphora"},
  "cache": {"prefix_hit": true, "semantic_hit": false}
}
```

三条纪律：

1. **不存原文**，只存 id、长度、计数。否则 manifest 本身成为隐私漏洞。
2. **1% 采样 + 7 天保留**，成本可控。
3. **可回放**：给定 manifest + 消息表，能重放出同一份上下文。这是排障和 A/B 的前提。

---

### 20.12 明确不做（以及为什么）

这一节比"做了什么"更能体现判断力。面试官问"你还了解哪些方案"时，能说清"我评估过但没选"远好于"我都知道"。

| 技术 | 为什么不做 |
|---|---|
| 向量库（Milvus / pgvector / Qdrant） | 单会话检索规模是几百到几千条，bigram 倒排 + BM25 足够；引入向量库要多一套运维、一致性、备份。规模不匹配时，选型就是负债 |
| 微调 embedding | 没有标注数据，也没有稳定的评估集；微调后无法解释退化 |
| LLMLingua 式 token 压缩 | 压缩后无法保留原文 span，直接摧毁 §20.4 的归因；中文 tokenizer 下压缩率不稳定；省下的 token 换来不可审计，不划算 |
| 多 Agent 编排 | 单 Agent + 工具已经能覆盖；多 Agent 的协调成本、状态同步、失败重试复杂度远大于收益 |
| GraphRAG / 图数据库记忆 | 实体关系稀疏，构图成本高；"用户—订单—商品"这种结构用关系表更直接 |
| 端到端 RL 优化上下文策略 | 没有可靠 reward，离线评估都做不稳，谈何在线学习 |
| 把记忆流做成 Kafka 主题 | 消息链路已经用了 Kafka；记忆是低频写、强一致读，套消息队列只会增加延迟与重复消费问题 |

**边界感本身就是能力。** 大二能说清"我为什么不用向量库"，比"我搭了个向量库"更能说明你想过。

---

### 20.13 评测：没有这一节，上面全是故事

#### Golden set

50 条会话，四类各 12–13 条：

| 类型 | 构造方式 | 考察 |
|---|---|---|
| 事实保持 | 第 3 轮给出订单号，第 40 轮追问 | 长程事实召回 |
| 指代消解 | "那它呢" / "换成那个" | 检索触发与指代解析 |
| 冲突更新 | 第 10 轮说预算 5000，第 30 轮改成 8000 | 新旧事实裁决 |
| 超长会话 | 200+ 轮，含工具调用与失败重试 | 压缩与降级 |

#### 指标

| 指标 | 定义 | 方向 |
|---|---|---|
| `fact_recall@1` | 关键事实是否出现在注入上下文中 | 越高越好 |
| `stale_fact_rate` | 注入的事实是否是已被覆盖的旧值 | 越低越好 |
| `answer_groundedness` | 回答中的事实能否在上下文中找到依据 | 越高越好 |
| `tokens_per_turn` | 每轮实际输入 token | 越低越好（同质量下） |
| `ttft_p50` | 首 token 延迟 | 越低越好 |
| `degradation_rate` | 触发降级的请求占比 | 越低越好 |

#### 统计纪律

1. 每条用例跑 3 次取中位数，模型有随机性，单次结果不可信。
2. 样本 < 30 不下结论；50 条只能说"有信号"，不能说"显著"。
3. LLM-as-judge 只做初筛，人工抽检 20%；judge 的 prompt 和模型版本要固定。
4. 报告 delta 时必须同时报告基线绝对值，否则"提升 20%"可能只是从 0.1 到 0.12。
5. A/B 只改一个变量。同时换压缩策略和检索策略，出了差异说不清是谁的贡献。

---

### 20.14 落地顺序（叠加到 §16）

不打断原计划，增强项插在阶段之间：

| 插入点 | 内容 | 工时 | 前置 |
|---|---|---|---|
| P1.5 | A2 约束解码 + A3 Span 归因 | 1.5 天 | P1 事实槽完成 |
| P2.5 | B3 cross-encoder | 0.5 天 | P2 检索层完成 |
| P3.5 | A1 前缀稳定性 + D2 Manifest | 1.5 天 | P3 配额完成 |
| P4.5 | C1 离线整合 | 2 天 | P4 状态外置完成 |
| 有余力 | B1 / B2 / C2 / D1 | 按需 | 对应指标证明有收益 |

原则：**A2/A3/D2 先做**，因为它们补的是"正确性"和"可解释性"；B1/B2/C2/D1 是"效果增强"，必须有 A/B 才上。

---

### 20.15 面试讲法速查

| 点 | 一句话 | 最可能的追问 | 回答抓手 |
|---|---|---|---|
| A1 前缀缓存 | 先算预算再排版，稳定性决定推理成本 | 命中率多少 | 给口径：cache_read/input，长会话目标 >50%，实测另说 |
| A2 约束解码 | 语法层不可能出错，且 decoder 要报告是否真的约束 | 模型填默认值怎么办 | `unknown` 枚举 + 下游当缺失 + repair_rate 告警 |
| A3 Span 归因 | 记忆可回指，撤回能精确失效 | 模型改写了原文 | 存 quote 不存 value，三级校验，数字精确匹配 |
| B1 Contextual Retrieval | 块脱离文档不可读，补一句定位上下文 | 成本怎么办 | 文档进缓存前缀，只有块在变 |
| B2 Late Chunking | 先嵌入再切块，块向量看过全文 | 和 B1 怎么选 | 看 BM25 贡献；默认 B1，B2 做 A/B |
| B3 Rerank | RRF 管可比性，rerank 管相关性 | 提升多少 | 先 recall@20 再 nDCG@10，报自己的 delta |
| C1 离线整合 | 记忆系统的 LSM compaction | 怎么不覆盖新数据 | version 水位线 + CAS + superseded_by |
| C2 层次摘要 | 把粒度变成可检索维度 | 什么时候值得 | P95 > 150 轮才做 |
| D1 投机预取 | 用统计换延迟，三个护栏 | 会不会浪费 | 可取消 + 每轮 1 次 + 命中率自适应熔断 |
| D2 Manifest | 装配过程可回放 | 隐私怎么办 | 不存原文，只存 id/长度/计数，1% 采样 |

---

### 20.16 本章新增的不变量

接 §4 的 I1–I7，增强层必须满足：

| 编号 | 不变量 | 违反后果 |
|---|---|---|
| I8 | 进入前缀的内容必须通过 `Canonical()`，且不含时间戳/随机数/无序集合/per-user 数据 | 缓存全失效，或跨用户泄漏 |
| I9 | `Allocate()` 与 `Layout()` 必须分离；Layout 不得改变任何层的 token 配额 | 排布优化悄悄挤掉语义层 |
| I10 | 结构化输出必须显式报告"是否真的施加了约束" | 静默降级，解析失败被当成成功 |
| I11 | 事实槽必须能回指到 `(msg_id, start, end)` 的原文；`Value` 与 `Quote` 不一致时不得注入 | 幻觉被当成事实 |
| I12 | 任何增强都必须有独立开关与回滚路径，关掉后系统行为回到基线 | 增强变成不可拆除的耦合 |

---

### 20.17 一句话总结

核心层解决"上下文怎么组装才正确"，增强层解决"同样的正确，能不能更便宜、更准、更可解释"。

**先正确，再便宜，最后才是聪明。** 顺序反了，就是拿复杂度换故事。

---

## 附录 A：与现有代码的对应关系

| 现有实现 | 改造后 | 动作 |
|---|---|---|
| `packMessagesByBudget` | `ctxmem.Assemble` 的 Working 装配 | 删除，逻辑迁入 |
| `ConversationCompressor` | Episodic 层 | 保留结构，改输出契约 |
| `summaryState`（进程内） | Redis `SummaryState` | 替换 |
| `trimHistory` | 并入 Assembler，只保留孤儿修正 | 保留函数，改为校验器 |
| `historyNotice` | `Assembly.Omitted` 渲染 | 保留，扩展为分层报告 |
| `toSchemaMessages` | 删除 | 其"同角色去重"是 bug 来源 |
| `buildMemoryEnhancedSystemPrompt` | per-user 消息注入 | 改写 |
| `estimateTokenCount` | `TokenEstimator` 默认实现 | 保留算法，抽接口 |
| `selectMemories` | 不变 | 仅调整预算入口 |

---

## 附录 B：一句话总结

现有实现把"上下文"当成一个装消息的桶，按时间往里塞；
改造后把它当成四层内存，每层有自己的写入时机、保真要求和预算来源。

真正新增的能力只有两个：**结构化事实槽**和**会话内检索**。
其余六个阶段都是在把已有的正确想法（滚动摘要、省略告知、三因子记忆）放回它们该在的层里。
