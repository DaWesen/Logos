# Logos 语义缓存 v2 设计与实施规范

> 交接文档。读者是负责实现的下一位工程师或 AI Agent。
> 本文默认你**不熟悉本仓库**，所有结论都附了真实文件与行号，可自行核对。
> 本文不是提案，是施工图：照做即可，不需要再设计一遍。若发现本文与代码冲突，以代码为准，并在 PR 里修正本文。

---

## §0 交接须知

### 0.1 这份文档要解决什么

`internal/service/ai/bot/service/semantic_cache.go` 目前已经上线在主链路上，但它是一个"能跑通、不能上生产"的实现：**缓存键缺版本维度、单一阈值未校准、存储结构是线性扫描、写入无准入**。其中至少三条会导致用户看到**自信但错误的答案**，比不使用缓存更糟。

本规范把它升级为一个可解释、可标定、可灰度、可回滚的模块。

### 0.2 硬约束

| 编号 | 约束 | 说明 |
|---|---|---|
| C1 | 只改语义缓存及相关装配点 | 不动 IM 消息链路、不动 planner、不动 RAG 检索算法 |
| C2 | 缓存任何失败都必须降级为"未命中" | 不得向用户抛错，不得阻塞主链路 |
| C3 | 不允许新增 CGO 依赖 | 部署环境是纯 Go 构建 |
| C4 | 不允许新建 Redis 连接池 | 必须复用 `pkg/cache` 的单例（见 §9.3） |
| C5 | 所有阈值必须可配置 | 禁止再出现 `const semanticCacheThreshold = 0.90` |
| C6 | 所有"性能/命中率"数字必须标注来源 | 未实测的一律写"待标定"，禁止编造 |

### 0.3 验收方式

见 §17。**未通过 §17 全部九条，不得合入。**

### 0.4 禁止事项（摘要）

见 §18。最容易犯的三个：把 embedding 调用放在探活之前、把工具调用结果缓存、用 `SCAN + DEL` 做失效。

---

## §1 现状（必须逐条核对）

### 1.1 代码地图

| 位置 | 作用 |
|---|---|
| `internal/service/ai/bot/service/semantic_cache.go:20-26` | 五个硬编码常量：前缀、每用户条数 50、key TTL 24h、条目 TTL 12h、阈值 0.90、拨号冷却 24h |
| `semantic_cache.go:37-44` | `SemanticCache` 结构体：持有自己的 `*redis.Client` 与 `lastFail` |
| `semantic_cache.go:55-60` | `semanticEntry{Query, Answer, Vector, CreatedAt}` |
| `semantic_cache.go:69-71` | key 生成：`logos:scache:{userID}:{botID}` |
| `semantic_cache.go:74-102` | 惰性拨号 + 24h 冷却 |
| `semantic_cache.go:105-160` | `Lookup`：embed → LRANGE 全量 → 逐条余弦 → 取最大 |
| `semantic_cache.go:165-169` | Lua：LPUSH + LTRIM + EXPIRE |
| `semantic_cache.go:171-213` | `Store` |
| `semantic_cache.go:224-240` | `cosineSimilarity`（纯函数） |
| `semantic_cache.go:242-265` | `embedForBot`：Bot 自定义 embedding 优先，回退全局 |
| `semantic_cache.go:267-276` | `storeSemanticCache`：起 goroutine，`context.Background()` + 10s |
| `internal/service/ai/bot/service/bot.go:176` | `botServiceImpl.semanticCache` 字段 |
| `bot.go:227-232` | 装配点：`cfg != nil` 时创建 |
| `bot.go:929` | 非流式：**保存用户消息之后、创建 Agent 之前**查缓存 |
| `bot.go:1057` | 非流式：成功路径写缓存 |
| `bot.go:1140` | 流式：同样位置查缓存，命中则 `onChunk(整段)` |
| `bot.go:1180 / 1220 / 1232` | 流式：Planner 路径 / 无历史路径 / 常规路径三处写缓存 |
| `config/config.go:143-149` | `config.Redis{Host,Port,Password,DB,PoolSize}` |
| `pkg/cache/redis.go:241 / 265` | 既有 Redis 单例 `NewRedisCache()` / `InitRedis()`，含指数退避启动重试（13s 内放弃） |
| `pkg/obs/metrics.go:869-880` | `RecordSemanticCache(ctx, op, result)` |

### 1.2 现有调用链

```
SendMessage / SendMessageStream
  → repo.AddMessage(user)                    # 先落库
  → semanticCache.Lookup(bot, userID, content)
       ├─ embed(query)                       # 一次 embedding API
       ├─ getClient()                        # 可能拨号 / 24h 冷却
       └─ LRANGE key 0 49                    # 全量 50 条
            → json.Unmarshal × 50
            → cosineSimilarity × 50
            → max ≥ 0.90 ? 返回 : miss
  → hit: saveAssistantMessage(cached); return   # 不计费、不写 seq 之外任何东西
  → miss: agent/planner 正常执行
  → 成功后 storeSemanticCache(...)           # goroutine, 无准入
```

### 1.3 现有测试覆盖

`semantic_cache_test.go` 共 8 个用例，覆盖：余弦纯函数、nil 接收者、embedding 失败降级、`toFloat32`。

**未覆盖**：key 隔离、版本失效、TTL 语义、并发写、假命中、灰度开关。也就是说，所有会出事的路径都没测。

---

## §2 缺陷清单

严重度：**S1** = 会向用户返回错误答案或造成数据泄漏；**S2** = 可用性/性能/运维问题；**S3** = 可维护性。

### D1 [S1] 缓存键缺少版本维度

**证据**：`semantic_cache.go:69-71`

```go
func semanticCacheKey(userID, botID string) string {
	return fmt.Sprintf("%s:%s:%s", semanticCacheKeyPrefix, userID, botID)
}
```

**后果**：以下任一变更后，旧答案继续命中，最长 12 小时：

- Bot 修改 `system_prompt`（人设、语气、规则全变）
- Bot 切换 `model_name` / `model_provider`
- `bot.EmbeddingModel` 变更
- 关联知识库增删文档（`autoSaveToKnowledgeBase` 在 `bot.go:1057` 之后立刻发生，**自己刚入库的新内容反而查不到，只拿到缓存**）
- 全局默认 prompt 模板升级

这是最严重的一条：用户改完人设发现"机器人还是原来的语气"，且无法解释。

### D2 [S1] 多轮上下文不参与判定

**证据**：`Lookup(ctx, bot, userID, query)` 只接收 `content`。

**后果**：同一用户在同一 Bot 下，对话 A 问"这个怎么配置"得到的答案，会命中对话 B 里同样四个字的问题。指代词（"这个""它""第二个"）在向量空间里与任何短句都高度相似。

`semanticEntry.Query` 字段存了原文，但**从未参与任何比较**（`semantic_cache.go:143-149` 只比较向量）。

### D3 [S1] 单一阈值 0.90，硬编码，未校准

**证据**：`semantic_cache.go:25`、`:152`。

**后果**：

- 阈值与 embedding 模型强耦合。换模型（`text-embedding-3-small` → `bge-m3`）后相似度分布整体平移，0.90 可能对应完全不同的精确率。
- 没有任何负样本验证。"关闭自动续费" vs "开启自动续费" 在主流 embedding 上相似度约 0.95，**必然假命中**。
- 阈值不可配，A/B 与灰度都做不了。

### D4 [S1] 没有槽位门控

否定词、数量词、实体顺序，语义向量几乎不敏感。当前实现没有任何结构化闸门，全靠一个标量阈值兜底。

典型翻车（都是"高相似、反答案"）：

| 类型 | 例 |
|---|---|
| 否定翻转 | 如何**关闭**自动续费 / 如何**开启**自动续费 |
| 实体互换 | **上海到北京**机票 / **北京到上海**机票 |
| 数字漂移 | **2024** 年个税起征点 / **2026** 年 |
| 范围翻转 | **小于** 500 元怎么开票 / **大于** 500 元 |

### D5 [S1] 未记录 embedding 模型与维度，换模型后向量混比

**证据**：`semanticEntry`（`semantic_cache.go:55-60`）只有 `Query/Answer/Vector/CreatedAt`。

**后果**：`cosineSimilarity` 在维度不等时返回 0（`semantic_cache.go:225-227`），看起来"安全"。但若新旧模型维度相同（例如都是 1024 维），就会在**两个不同的向量空间之间计算余弦**，得到一个无意义的数，**随机命中**。这是最隐蔽的一条。

### D6 [S2] List 线性扫描 + 全量反序列化

**证据**：`semantic_cache.go:129` LRANGE 取 0..49，`:139-150` 逐条 `json.Unmarshal` 后算余弦。

**成本**：1024 维 float32 以 JSON 数组存储，单条约 10~14KB；50 条约 0.5~0.7MB。**每次 Lookup 都要传输并解析半兆数据**，再算 5 万次浮点乘加。在 100 QPS 下就是 50MB/s 的 Redis 出流量，纯属浪费。

`Query` 字段被序列化进去但从不使用，进一步放大体积。

### D7 [S2] 逻辑过期条目不清除，仍占名额

**证据**：`semantic_cache.go:143-145` 用 `CreatedAt` 过滤，但不删除。

**后果**：条目 12h 后失效，但仍在 List 里占位到 24h key 过期。期间 List 被 50 条死数据填满，新条目被 LTRIM 挤掉，**命中率断崖**，而 LRANGE 成本一分不省。

### D8 [S2] 写入无准入，脏答案进缓存

**证据**：`semantic_cache.go:171-213` 只校验 `query != "" && answer != ""`。

会被缓存的内容包括：

- "抱歉，我无法回答这个问题"
- 工具执行失败后的兜底文案
- Plan-and-Execute 的多步结果（`bot.go:1180`）
- 强时效内容（"今天天气""当前库存"）
- 含用户隐私的个性化答案
- 触发 prompt 注入护栏后的固定回复（`bot.go:1170` 之后的注入路径）

### D9 [S2] 写入 goroutine 无上限，miss 无合并

**证据**：`semantic_cache.go:267-276` 每次成功对话起一个 goroutine。

**后果**：

- 高 QPS 下 goroutine 与 Redis 连接竞争无界。
- 同一 query 并发 100 个请求会同时 miss、同时调 LLM、同时写 100 条相同条目。**缓存最该防的惊群，这里完全没防。**

### D10 [S2] 自建 `redis.Client`，绕过 `pkg/cache`，且冷却 24 小时

**证据**：`semantic_cache.go:82-102`；对比 `pkg/cache/redis.go:33-52` 的指数退避（约 13s 内放弃）。

**后果**：

- 多一个连接池、多一份配置解析、多一处 Close 遗漏。
- Redis 若比应用晚 30s 就绪，缓存直接瘫痪 24 小时，且只有一条 metric 提示，没有告警。
- 与项目既有 Redis 治理（`op.go` 的超时包装、`ttl.go` 的抖动）完全脱节。

### D11 [S2] 先调 embedding，后探活

**证据**：`semantic_cache.go:110-118` 先 `c.embed(...)`，`:120` 才 `getClient`。

**后果**：Redis 挂了、缓存被关、冷却期内，**每一次请求仍然白付一次 embedding API 调用**。顺序反了。

### D12 [S3] 命中路径不可观测

**证据**：`bot.go:930-936` 命中后只写一条 Info 日志，响应体里没有任何标记。

**后果**：前端无法展示"来自缓存"；计费无法区分；线上出现争议答案时无法定位是不是缓存导致；**假命中率这个最关键的指标根本无从计算**。

### D13 [S2] 与知识库自动入库冲突

**证据**：`bot.go:1057` 写缓存，`bot.go:1058` 之后 `auto_save_to_kb` 异步入库。

**后果**：新知识入库后，同一问题仍命中旧答案，直到 12h 后条目过期。用户视角是"我刚喂给它的资料它不认"。

### D14 [S3] 无开关、无灰度、无 per-Bot 配置

**证据**：`bot.go:227` 只要 `cfg != nil` 就启用，`bot.Config` 无相关键。

**后果**：无法对单个 Bot 关闭；无法先影子运行再放量；出问题只能回滚代码。

### D15 [S3] 流式命中一次性推全文

**证据**：`bot.go:1141-1144` `onChunk(cached)` 一次推完整答案。

**后果**：与正常流式的节奏差异极大，前端会闪一下全文，用户感知为"卡住然后突然出现"。需要伪流式回放（§6.3）。

### D16 [S2] 同用户并发写存在丢条目

**证据**：`semantic_cache.go:165-169` 的 Lua 是 LPUSH + LTRIM + EXPIRE，本身原子。但 `Store` 先 `embed` 再 `Run`，两个请求的 embed 完成顺序与 LPUSH 顺序无关，**List 内条目的时间序与 `CreatedAt` 序不一致**；`Lookup` 取"相似度最大"而非"最新"，反而放大了旧答案的权重。

### D17 [S3] 指标维度不足

**证据**：`pkg/obs/metrics.go:869-880` 只有 `op × result` 计数。

**缺失**：相似度分布直方图（标定必需）、判官调用与结论、存储条数、条目年龄、命中延迟、被拒写入原因。

---

## §3 目标与非目标

### 3.1 目标

| 编号 | 目标 | 可验证形式 |
|---|---|---|
| G1 | **版本正确性** | 任何影响答案的输入变化后，旧条目必须不可达（§11） |
| G2 | **假命中率可控且可测** | 标定集上假命中率 < 1%；线上有 `false_hit_suspect` 指标 |
| G3 | **读路径低延迟** | 不含 embedding 的判定 P99 < 5ms（单实例、10 万条目内） |
| G4 | **永远可降级** | 任意依赖故障 → 返回 miss，主链路无感（§6.5） |
| G5 | **可灰度** | `off / shadow / on` 三态，shadow 只记录不返回（§14） |
| G6 | **可解释** | 每次判定返回 `Decision` 枚举 + `reason` 字段，写入日志与 metric |
| G7 | **可标定** | 阈值来自 §10 的脚本输出，不来自人工估计 |
| G8 | **内存有界** | 条目数、字节数双上限，超出按 LRU 淘汰（§12.4） |
| G9 | **复用既有基础设施** | Redis 走 `pkg/cache` 单例；指标走 `pkg/obs`；日志走 `pkg/logger` |
| G10 | **调用点改动最小** | `bot.go` 的四处 Lookup、四处 Store 只改参数，不改控制流 |

### 3.2 非目标（明确不做，避免范围蔓延）

| 编号 | 不做 | 理由 |
|---|---|---|
| N1 | 跨用户共享缓存 | 答案可能含个人数据；命中率收益不值得冒泄漏风险 |
| N2 | 缓存 embedding 结果 | 属于另一层（embedding 层缓存），单独设计 |
| N3 | 训练/微调相似度模型 | 数据量不够，维护成本高于收益 |
| N4 | 多实例强一致 | 缓存允许最终一致；用版本前缀实现近似失效即可 |
| N5 | 分布式锁 | 用 singleflight 解决单实例惊群，跨实例重复计算可接受 |
| N6 | 缓存工具调用结果 | 副作用不可重放，见 §18 |

---

## §4 架构与不变量

### 4.1 判定流水线

```
                ┌─────────────────────────────────────────────┐
 query ────────►│ 1. normalize  去空白 / 全半角 / 标点 / 大小写 │
                └───────────────┬─────────────────────────────┘
                                ▼
                ┌─────────────────────────────────────────────┐
                │ 2. exact hash (Redis GET, 1 RTT)            │──hit──► HitExact
                └───────────────┬─────────────────────────────┘
                                ▼
                ┌─────────────────────────────────────────────┐
                │ 3. scope resolve                            │
                │    bot / kbVer / promptHash / model / embed │
                └───────────────┬─────────────────────────────┘
                                ▼
                ┌─────────────────────────────────────────────┐
                │ 4. slot extract + gate   (纯本地, 0 RTT)     │──mismatch──► MissSlot
                └───────────────┬─────────────────────────────┘
                                ▼
                ┌─────────────────────────────────────────────┐
                │ 5. embed(query)   (1 次外部调用, 带超时)      │
                └───────────────┬─────────────────────────────┘
                                ▼
                ┌─────────────────────────────────────────────┐
                │ 6. vector top-k   (进程内索引, 0 RTT)        │
                └───────────────┬─────────────────────────────┘
                                ▼
                  sim ≥ high ───┴─── high > sim ≥ mid ───┐
                       │                                 ▼
                       │                    ┌────────────────────────┐
                       │                    │ 7. judge (≤300ms)      │
                       │                    └───────┬────────────────┘
                       ▼                            ▼
                  HitVector                  HitJudge / MissJudge
```

`sim < mid` → `MissLowSim`。

### 4.2 五条不变量（实现时逐条自检）

| 编号 | 不变量 | 违反后果 |
|---|---|---|
| I1 | **任何影响答案的输入，必须出现在 scope 里或被 gate 拦截** | D1、D2 复活 |
| I2 | **缓存路径的任何错误 = miss**，绝不向上传播 | 用户看到 500 |
| I3 | **写是 best-effort，读不等待写**；写失败只记 metric | 主链路延迟抖动 |
| I4 | **失效靠版本前缀 + TTL，不做扫描删除** | 大 key 阻塞 Redis |
| I5 | **条目不可变**；更新 = 新增一条 + 旧条目标记失效，不就地改写 | 并发读到半写状态 |

---

## §5 数据模型

### 5.1 Scope：决定"能不能复用"的全部维度

```go
// internal/service/ai/bot/service/scache/scope.go
package scache

type Scope struct {
	Tenant     string // 预留；当前单租户填 "default"
	BotID      string
	UserID     string // N1：不做共享，必须非空
	KBVersion  string // 见 §11.1
	PromptHash string // sha256(system_prompt)[:12]
	ModelKey   string // provider + "/" + model_name + "/t=" + temperature
	EmbedKey   string // provider + "/" + model_name + "/d=" + dim
}

// Hash 返回 16 字节十六进制，用于拼 bucket key。
// 顺序敏感：字段拼接必须用不可出现在字段内的分隔符（"\x1f"）。
func (s Scope) Hash() string
```

**规则**：

- `UserID` 为空直接拒绝缓存（防止越权复用）。
- `PromptHash` 用**规范化后**的 prompt（去首尾空白、统一换行）计算，避免格式化差异导致无谓失效。
- `ModelKey` 必须含 temperature：`temperature > 0` 的 Bot 默认**不缓存**（见 §18），但 key 里仍要带上，便于将来放开。
- `EmbedKey` 必须含维度。D5 的防线在这里。

### 5.2 Bucket key 布局

```
logos:sc:v2:{scopeHash16}          # Hash: field=entryID, value=Entry JSON
logos:sc:v2:{scopeHash16}:idx      # ZSet: member=entryID, score=CreatedAt（LRU 淘汰用）
logos:sc:v2:meta:{scopeHash16}     # Hash: 人类可读的 scope 字段，排障用，TTL 更长
logos:sc:v2:exact:{scopeHash16}    # Hash: field=normQueryHash, value=entryID（L0 精确命中）
```

**为什么用 Hash 而不是 List**：Hash 支持 `HDEL` 单条精确失效、支持 `HLEN` 计数、不会因为 LTRIM 误删新条目。List 只能整体重建。

**为什么 exact 单独一个 Hash**：精确命中是最高频、最便宜的路径，一次 `HGET` 就够，不必走向量。

### 5.3 Entry

```go
type AnswerKind string

const (
	AnswerFinal    AnswerKind = "final"    // 正常生成，可缓存
	AnswerDegraded AnswerKind = "degraded" // 兜底/道歉/转人工，禁止缓存
)

type Entry struct {
	ID        string     `json:"id"`
	Query     string     `json:"q"`   // 原文，排障与判官用
	NormQuery string     `json:"nq"`  // 归一化后
	Slots     SlotSet    `json:"slots"`
	Answer    string     `json:"a"`
	Kind      AnswerKind `json:"kind"`
	Vec       []float32  `json:"v"`   // dim 见下；若用二进制编码另议（§9.2）
	Dim       int        `json:"dim"`
	EmbedKey  string     `json:"ek"`  // 冗余存一份，反序列化后可自校验
	ScopeHash string     `json:"sh"`
	CreatedAt int64      `json:"c"`
	ExpireAt  int64      `json:"e"`
	Hits      int32      `json:"h"`
	Source    string     `json:"src"` // 生成该答案的 model，审计用
}

// Valid 在反序列化后调用，任何一项不符即视为损坏条目并跳过。
// 这里是 D5 的第二道防线：即使 key 拼错，也不会拿错维度的向量去比。
func (e *Entry) Valid(scopeHash string, dim int) bool
```

### 5.4 SlotSet

```go
type SlotSet struct {
	Negated  bool     // 是否含否定词
	Intent   string   // 归一化意图标签，如 "howto" / "price" / "policy"
	Entities []string // 排序去重后的实体
	Numbers  []string // 数字与单位，如 "500元" / "2026"
	TimeRef  string   // "今天" / "2026-11" / ""（无时间指代）
	Compare  string   // "gt" / "lt" / "eq" / ""
}

// Compatible 判断两个槽位集合是否允许复用答案。
// 采用"保守否决"：任何一项不可比较或不确定，返回 false。
func (a SlotSet) Compatible(b SlotSet) bool
```

### 5.5 Decision

```go
type Decision string

const (
	HitExact     Decision = "hit_exact"
	HitVector    Decision = "hit_vector"
	HitJudge     Decision = "hit_judge"
	MissSlot     Decision = "miss_slot"
	MissLowSim   Decision = "miss_low_sim"
	MissJudge    Decision = "miss_judge"
	MissNoSlots  Decision = "miss_no_slots" // 无法抽取槽位且 query 过短
	MissDisabled Decision = "miss_disabled"
	MissError    Decision = "miss_error"
	MissShadowed Decision = "miss_shadowed" // shadow 模式：本该命中但不返回
)
```

`Decision` 必须随响应日志与 metric 一起上报。**没有它，G2 就无法验证。**

---

## §6 主流程

### 6.1 Lookup 伪代码（顺序不可调换）

```go
func (c *Cache) Lookup(ctx context.Context, req LookupRequest) (LookupResult, error) {
	// 0. 开关与前置条件（0 成本）
	if c.mode == ModeOff || req.UserID == "" || req.Query == "" {
		return result(MissDisabled), nil
	}

	// 1. 归一化（纯本地）
	nq := Normalize(req.Query)
	if nq == "" {
		return result(MissNoSlots), nil
	}

	scope := BuildScope(req)
	sh := scope.Hash()

	// 2. 精确命中：一次 HGET，最便宜，必须先做
	if entry, ok := c.exactGet(ctx, sh, nq); ok {
		return result(HitExact, entry), nil
	}

	// 3. 槽位抽取与门控：纯本地，0 外部调用
	//    放在 embed 之前，是为了让"注定不命中"的请求不花 embedding 钱
	slots := c.extract(req.Query)
	if slots.Empty() && TooShortOrAnaphoric(nq) {
		// "这个呢""然后呢""第二个" 这类，脱离上下文无意义
		return result(MissNoSlots), nil
	}

	// 4. embedding：唯一的外部调用，必须有超时
	vec, err := c.embed(ctx, scope, req.Query)
	if err != nil || len(vec) == 0 {
		return result(MissError), nil
	}

	// 5. 向量检索：进程内索引，拿 top-k（k 取 5，便于判官看上下文）
	cands := c.index.Search(sh, vec, 5)
	if len(cands) == 0 {
		return result(MissLowSim), nil
	}

	best := cands[0]
	// 6. 槽位兼容性：向量高相似但槽位冲突，直接否决
	if !slots.Compatible(best.Slots) {
		return result(MissSlot), nil
	}

	switch {
	case best.Sim >= c.cfg.HighThreshold:
		c.touch(ctx, best)
		return result(HitVector, best), nil
	case best.Sim >= c.cfg.MidThreshold:
		// 7. 灰区判官：有界超时，失败按 miss
		verdict := c.judge(ctx, req.Query, cands)
		if verdict == JudgeSame {
			return result(HitJudge, best), nil
		}
		return result(MissJudge), nil
	default:
		return result(MissLowSim), nil
	}
}
```

**顺序理由（面试会问，必须能答）**：

1. 开关在最前：关掉时零成本。
2. 精确命中：一次 RTT，跳过后面全部开销。
3. 槽位门控：纯 CPU，放在 embedding 之前，把"注定不命中"的请求挡在付费调用之外（同时修掉 D11 的反向问题）。
4. embedding：唯一网络调用。
5. 向量检索：本地。
6. 判官：最贵，只在灰区。

### 6.2 Store 伪代码

```go
func (c *Cache) Store(ctx context.Context, req StoreRequest) {
	// 1. 准入检查（§6.4），任何一条不满足直接丢弃并记 store_rejected 原因
	if reason := c.admissible(req); reason != "" {
		obs.RecordSCacheStore("rejected_" + reason)
		return
	}

	scope := BuildScope(req)
	slots := c.extract(req.Query)
	if slots.Empty() && TooShortOrAnaphoric(Normalize(req.Query)) {
		obs.RecordSCacheStore("rejected_no_slots")
		return
	}

	// 2. 归一化 + embedding（复用 Lookup 阶段已算出的向量，避免重复调用）
	vec := req.Vector
	if vec == nil {
		var err error
		vec, err = c.embed(ctx, scope, req.Query)
		if err != nil {
			obs.RecordSCacheStore("embed_error")
			return
		}
	}

	// 3. 入有界队列，满了直接丢（缓存是可选优化，不值得排队）
	select {
	case c.writeCh <- buildEntry(scope, slots, req, vec):
	default:
		obs.RecordSCacheStore("dropped_queue_full")
	}
}
```

worker 侧：

```go
func (c *Cache) writeLoop() {
	for e := range c.writeCh {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		// HSET + ZADD + EXPIRE 用 pipeline 一次往返
		if err := c.persist(ctx, e); err != nil {
			obs.RecordSCacheStore("persist_error")
		}
		c.index.Insert(e.ScopeHash, e) // 本地索引总是插入，即使 Redis 失败
		cancel()
	}
}
```

**注意**：本地索引与 Redis 是两个副本。Redis 失败不影响本实例命中，只影响重启后的冷启动与其他实例。这是有意为之（I3）。

### 6.3 伪流式回放（修 D15）

缓存命中时不能一次性 `onChunk(整段)`。按句/标点切块，每块之间 sleep，总时长上限 400ms：

```go
func replayAsStream(answer string, onChunk func(string) error) error {
	chunks := splitForReplay(answer, 24) // 按 rune 数切，优先在标点后断
	delay := 400 * time.Millisecond / time.Duration(len(chunks))
	if delay > 15*time.Millisecond {
		delay = 15 * time.Millisecond
	}
	for _, ch := range chunks {
		if err := onChunk(ch); err != nil {
			return err // 客户端断开，直接返回
		}
		time.Sleep(delay)
	}
	return nil
}
```

参数说明：24 rune 是"接近逐 token 的观感"，400ms 是"用户不觉得慢"的上限。这两个值放配置，允许调。

### 6.4 写入准入规则（修 D8）

`admissible` 必须全部通过：

| 规则 | 判定 |
|---|---|
| 答案长度 | `50 ≤ runes ≤ 4000` |
| 非兜底文案 | 不匹配 `无法回答 / 抱歉.*不能 / 转人工 / 出错了 / 请稍后重试` |
| 非注入护栏 | `req.InjectionGuardHit == false` |
| 非 planner 产物 | `req.Source != "planner"`（多步结果依赖外部状态，不复用） |
| 非工具调用轮 | `req.ToolCalled == false`（副作用不可重放） |
| temperature | `== 0` |
| 无时间指代 | `slots.TimeRef == ""` 或答案内不含相对时间词 |
| 非空非纯标点 | — |

理由：这些用例一旦缓存，用户会连续多次拿到同一个错误结果，且刷新无效。

### 6.5 降级矩阵

| 故障 | 行为 | metric |
|---|---|---|
| Redis 不可用 | 仅精确命中失效；本地索引仍可命中 | `lookup{result=hit_vector}` 正常 |
| embedding 超时 | miss | `lookup{result=miss_error}` |
| 判官超时 | 按 miss（保守） | `judge{verdict=timeout}` |
| 本地索引为空（冷启动） | 从 Redis 懒加载；加载失败则纯精确命中 | `coldstart{result=fail}` |
| 配置非法（mid > high） | 启动时 panic（配置错误不能带病运行） | — |
| 写入队列满 | 丢弃 | `store{dropped_queue_full}` |

---

## §7 槽位门控

### 7.1 设计原则

槽位的作用不是"理解问题"，而是**用最便宜的手段排除掉向量无法区分的情况**。所以：

- 只抽**高置信、可枚举**的槽位，抽不到就留空。
- 比较采用**保守否决**：任一槽位一方有值另一方无值，且该槽位属于"强区分"类，判为不兼容。
- 抽取必须是纯本地、微秒级；**不允许为此调用模型**（灰区判官是唯一的模型调用）。

### 7.2 抽取规则

| 槽位 | 实现 | 强区分 |
|---|---|---|
| `Negated` | 词表匹配：不/别/勿/未/没/禁止/关闭/取消/停用/退订 vs 开启/启用/开通/订阅/恢复 | 是 |
| `Numbers` | 正则 `\d+(\.\d+)?\s*(元|块|万|亿|%|天|小时|个|次|年|月|日)?`，归一化单位后排序 | 是 |
| `Compare` | 大于/超过/高于 → `gt`；小于/低于/不足 → `lt` | 是 |
| `TimeRef` | 今天/明天/昨天/本周/本月/今年/现在/最新 → 归一化为类别，不展开成绝对日期 | 是 |
| `Entities` | 复用现有 NER 或 knowledge 包的实体抽取；抽不到留空 | 是（仅当双方都非空时比较） |
| `Intent` | 关键词规则：怎么/如何 → `howto`；多少钱 → `price`；能不能 → `feasibility` | 否（仅用于排序加权） |

### 7.3 比较规则

```go
func (a SlotSet) Compatible(b SlotSet) bool {
	if a.Negated != b.Negated {
		return false // 否定翻转，最高优先级
	}
	if a.Compare != "" && b.Compare != "" && a.Compare != b.Compare {
		return false
	}
	if a.TimeRef != "" && b.TimeRef != "" && a.TimeRef != b.TimeRef {
		return false
	}
	if (len(a.Numbers) > 0 || len(b.Numbers) > 0) && !sameSet(a.Numbers, b.Numbers) {
		return false
	}
	if len(a.Entities) > 0 && len(b.Entities) > 0 && !sameSet(a.Entities, b.Entities) {
		return false
	}
	return true
}
```

**关键的"不对称"规则**：一方有槽位、另一方没有时——

- `Negated`：必须相等（默认 false），所以"关闭 X"不会命中"X 怎么用"。
- `Numbers` / `Entities`：**一方为空则放行**。因为抽取器本身会漏，硬否决会误杀大量正常改写。
- `TimeRef`：一方为空则放行，理由同上。

这条不对称是经验判断，必须在 §10 的标定集上验证（用"漏抽"样本专门测）。

### 7.4 何时直接判 miss

`slots.Empty() && TooShortOrAnaphoric(nq)`：

- rune 数 < 8，且
- 命中指代词表（这个/那个/它/他/她/上面/刚才/第二个/然后/继续/还有呢）

这类 query 脱离上下文不可判定，缓存必然出错。

---

## §8 灰区判官

### 8.1 触发条件

仅当 `MidThreshold ≤ sim < HighThreshold` 且已找到候选时触发。频率应远低于命中次数；若判官调用量超过 lookup 的 30%，说明阈值设置有问题，回 §10 重新标定。

### 8.2 Prompt（`scache/judge_prompt.go`）

```
你是缓存命中判定器。给你一个"新问题"和若干"历史问题+答案"。
判断：历史答案能否原样回答新问题。

判定为 SAME 的条件（全部满足）：
1. 两个问题的诉求相同（不是"相关"，是"同一个诉求"）
2. 关键实体、数量、时间范围、否定与否完全一致
3. 历史答案不依赖当时的上下文（无"上面提到的""刚才说的"）
4. 答案中的时效信息仍然有效

任何一条不确定 → 判 DIFFERENT。宁可漏命中，不可错命中。

只输出一行：SAME 或 DIFFERENT，然后一个空格，接不超过 20 字的理由。

新问题：{query}
历史候选：
[1] 问题：{q1}
    答案：{a1}
[2] 问题：{q2}
    答案：{a2}
```

参数：`temperature=0`、`max_tokens=40`、超时 300ms。候选答案截断到 500 字再喂，避免判官被长文拖慢。

### 8.3 解析

```go
func parseVerdict(s string) Verdict {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(strings.ToUpper(s), "SAME"):
		return JudgeSame
	case strings.HasPrefix(strings.ToUpper(s), "DIFFERENT"):
		return JudgeDifferent
	default:
		return JudgeUnparsable // 按 DIFFERENT 处理
	}
}
```

**默认保守**：解析失败、超时、模型报错，一律按 `DIFFERENT`（miss）。

### 8.4 成本

输入约 300~600 token，输出 < 40 token。与一次完整生成（通常 1000+ token 输出）相比低一个数量级。但仍要打点统计，避免阈值设得过低导致判官成为新热点。

---

## §9 存储与检索

### 9.1 方案对比

| 方案 | 读延迟 | 多实例一致 | 改动量 | 依赖 | 结论 |
|---|---|---|---|---|---|
| A. Redis List 全量扫（现状） | 0.5MB 传输 + 50 次余弦 | 天然一致 | 0 | 无 | 淘汰，D6 |
| B. Redis Hash + 进程内暴力扫 | 0 RTT（热数据） | 最终一致 | 中 | 无 | **P1 推荐** |
| C. 进程内 HNSW（纯 Go） | O(log n) | 需自建同步 | 大 | `github.com/coder/hnsw` | P2 可选 |
| D. 复用现有 Milvus | 1 RTT + 索引延迟 | 天然一致 | 中 | 已有 | P3 备选 |
| E. RediSearch KNN | 1 RTT | 一致 | 中 | 需 Redis 模块 | 受部署限制，不选 |

### 9.2 推荐：B（进程内索引 + Redis 持久化）

**结构**：

- 每个 `scopeHash` 在内存里一个 `[]*Entry`，上限 `MaxEntriesPerScope`（默认 200）。
- 检索就是遍历 + 点积。200 × 1024 维 ≈ 20 万次乘加，单核约 0.2ms，完全够用。
- Redis 只承担**持久化与冷启动**，不在热路径上。

**为什么不直接上 HNSW**：条目数 < 200/scope 时，暴力扫比 HNSW 更快（无图遍历开销、无内存碎片、缓存友好）。HNSW 在 1 万条以上才有优势。**先做简单的，等指标证明不够再换**——这句话要能对面试官说。

**向量编码**：JSON 数组（现状）体积大。改为 `[]float32` 的 base64 编码，体积降为原来的约 1/6（1024 维：13KB → 5.5KB）。若维度可降至 512 或使用 float16，可再降一半。

### 9.3 Redis 接入

**禁止** `redis.NewClient`。使用 `pkg/cache`：

```go
// 装配处（bot.go:227 附近）
sc := scache.New(scache.Config{
	Cache: cache.NewRedisCache(), // 进程内单例，见 pkg/cache/redis.go:241
	Embed: impl.embedForBot,
	Judge: impl.judgeChat,
	Mode:  cfg.SemanticCache.Mode,
	// ...
})
```

`pkg/cache` 已处理：启动重试（`redis.go:33-52`）、操作超时包装（`op.go:25`）、连接池复用。修 D10。

### 9.4 容量

```
内存 ≈ Σ_scope ( min(entries, MaxEntriesPerScope) × dim × 4B )
Redis ≈ 同上 × 1.5（JSON/base64 膨胀）
```

以 1 万活跃 scope、每 scope 200 条、1024 维计算：2,000,000 × 4096B ≈ 8GB，显然不可接受。

**因此必须设全局上限**：`MaxTotalEntries`（默认 50000）+ LRU 淘汰（§12.4）。按此计算：50000 × 1024 × 4B ≈ 200MB，可接受。

**注意**：`MaxEntriesPerScope` 与 `MaxTotalEntries` 必须同时生效，否则热门 Bot 会挤占全部内存。

---

## §10 阈值标定

> 这一节是整份文档里**唯一能证明"0.90 不是拍脑袋"的部分**，不能省略，也不能伪造结果。

### 10.1 数据集构造

目标规模 300 对，全部标注为"应命中 / 不应命中"。

| 类别 | 数量 | 构造方式 |
|---|---|---|
| 正样本：同义改写 | 120 | 换语序、加礼貌语、同义词替换、中英混写 |
| 正样本：省略主语 | 40 | "怎么开票" vs "发票怎么开" |
| 正样本：标点/全半角差异 | 40 | 应由 `Normalize` 直接吃掉，用于验证归一化 |
| 负样本：否定翻转 | 20 | 开启/关闭、可以/不可以 |
| 负样本：实体互换 | 20 | 地点、人名、产品名对调 |
| 负样本：数字漂移 | 20 | 金额、年份、数量 |
| 负样本：时间指代 | 20 | 今天/下个月/最新 |
| 负样本：指代短句 | 20 | "这个呢""第二个" |

**采集来源**：优先用线上真实 query（脱敏后），不足部分人工改写。禁止用模型自己生成全集——分布会失真。

**标注协议**：两人独立标注，冲突由第三人仲裁；记录 Cohen's kappa，低于 0.8 说明标注标准不清，需重写标准再来。

数据文件：`internal/service/ai/bot/service/scache/testdata/calib.jsonl`

```json
{"q1":"怎么关闭自动续费","q2":"自动续费如何取消","label":"hit"}
{"q1":"怎么关闭自动续费","q2":"怎么开启自动续费","label":"miss","type":"negation"}
```

### 10.2 标定工具

新增 `cmd/tools/scache-calibrate/main.go`（不进生产镜像）：

```
输入: calib.jsonl + embed 配置
输出: 终端表格 + calib_result.json

对 t ∈ [0.80, 0.99]，步长 0.005：
  TP = label=hit  且 sim ≥ t
  FP = label=miss 且 sim ≥ t
  FN = label=hit  且 sim <  t
  TN = label=miss 且 sim <  t
  输出 t, 命中率 TP/(TP+FN), 假命中率 FP/(FP+TN)
```

同时输出**负样本相似度分布的分位数**（p50/p90/p99）。理想情况下 p99 明显低于正样本 p10；若两条分布大面积重叠，说明这套 embedding 不适合作缓存，需要换模型或加判官。

运行方式（标定集需自行按 §10.1 采集，仓库不放真实数据）：

```bash
go run ./cmd/tools/scache-calibrate \
  -dataset internal/service/ai/bot/service/scache/testdata/calib.jsonl \
  -base-url https://api.openai.com/v1 -model bge-m3

go run ./cmd/tools/scache-calibrate -check -mid 0.88 -high 0.94 \
  -dataset internal/service/ai/bot/service/scache/testdata/calib.jsonl \
  -base-url https://api.openai.com/v1 -model bge-m3
```

`-check` 同时校验总体 FHR（默认 >1% 失败）与逐类硬负样本（任一类有假命中即失败），
避免总体比例被大量容易样本稀释。`testdata/calib.example.jsonl` 只演示 JSONL 格式，
不是标定数据，不能用于填 §10.3 的数字。

### 10.3 选点规则

```
候选集 = { t | FHR(t) < 0.01 }
high   = max(候选集)
mid    = max{ t | FHR(t) < 0.10 }   # 灰区下界，放宽到 10% 由判官兜底
```

若候选集为空 → 该 embedding 模型不可用于缓存，直接降级为"仅精确命中"，并在文档里记录结论。

**报告格式（写进 PR 描述）**：

```
embedding: bge-m3 (1024d)
样本: 300 (200 hit / 100 miss)
high=0.94 → HR=xx.x%  FHR=0.x%
mid =0.88 → HR=yy.y%  FHR=z.z%（其中判官拦截 w 例）
负样本 p99 = 0.93
```

数字必须来自脚本实际输出。**没有跑过就写"待标定"。**

### 10.4 阈值与模型绑定

`high/mid` 放进配置，并在 key 里带上 `EmbedKey`。换 embedding 模型 → `EmbedKey` 变 → 新 scope → 天然不会拿旧阈值套新模型。

配置里额外记录 `calibrated_for: "bge-m3/1024"`，启动时若与实际模型不符，打 WARN 并降级为 `mode=shadow`。

### 10.5 影子模式

上线顺序固定：

1. `mode=off`：不启用，确认无回归。
2. `mode=shadow`：完整执行判定，**但不返回缓存**，只记录"本会命中 + 相似度 + Decision"。持续 3~7 天。
3. 用 shadow 数据复核 §10.3 的选点，再切 `mode=on`，先对 1 个 Bot 放量。

shadow 的价值：**零风险拿到真实分布**。这一步能避免 90% 的上线事故。

---

## §11 失效与版本

### 11.1 KBVersion 的来源

三选一，按可行性排序：

| 方案 | 做法 | 评价 |
|---|---|---|
| A | Redis 计数器 `logos:kb:ver:{botID}`，知识库增删改时 `INCR` | **推荐**，O(1)，精确 |
| B | `sha256(sorted(docID + updatedAt))[:12]` | 无需额外状态，但要遍历文档元数据 |
| C | 定时刷新（每 5 分钟） | 有窗口期，不推荐 |

落地要求：`autoSaveToKnowledgeBase`（`bot.go:524`）与新文档入库路径必须调用 `BumpKBVersion`。**漏一个入口就等于 D13 没修。**

### 11.2 版本前缀失效

`scopeHash` 已包含 `KBVersion`，所以版本一变，bucket key 就是新的，旧数据**不可达**。

旧 bucket 不主动删除，靠 TTL 自然回收（I4）。这是"逻辑失效 + 物理过期"的组合，代价是短期内存翻倍，收益是**零阻塞**。

### 11.3 精确失效

除了版本前缀，还需要三种点对点失效：

| 场景 | 操作 |
|---|---|
| 单条答案被举报有误 | `HDEL bucket entryID` + `ZREM idx entryID` + 本地索引删除 |
| Bot 被删除 | 只需 TTL；`botID` 不会再被访问 |
| 用户要求"忘记我" | 按 `UserID` 扫？**不允许**。改为在 scope 里保留 `UserID`，删除时用 `meta` 索引反查（见下） |

`meta` 里额外存一份 `user → scopeHash` 的反向索引（Set），用户注销时按集合逐个 `DEL`，元素量级是个位数，可接受。

### 11.4 TTL 分档

| 内容类型 | TTL | 判定 |
|---|---|---|
| 制度/流程/文档问答 | 24h | 默认 |
| 产品价格/库存 | 30min | 命中 `价格/库存/优惠/活动` 关键词 |
| 个人数据相关 | **不缓存** | 含 `我的/账号/订单/余额` |
| 精确命中索引 | 与 bucket 同级 | — |
| `meta` | bucket × 2 | 排障用，留久一点 |

TTL 加抖动（`pkg/cache/ttl.go:13` 的 `JitterTTL`），避免同一时刻批量过期造成冷启动风暴。

---

## §12 并发与稳定性

### 12.1 惊群合并（修 D9）

同一 `scope + normQuery` 的并发请求只允许一个真正去生成，其余等待结果：

```go
import "golang.org/x/sync/singleflight"

// go.mod 中 golang.org/x/sync 目前是 indirect，需要提为 direct。
type Cache struct {
	group singleflight.Group
	// ...
}

// 在 miss 之后、调用 LLM 之前包一层：
v, err, shared := c.group.Do(scope.Hash()+":"+nq, func() (any, error) {
	return generateAnswer(ctx, req)
})
```

注意两点：

- **key 必须含 scope**，否则不同用户的同问题会被合并，造成越权。
- singleflight 只解决**单实例**内（N5）。跨实例重复生成可接受，不做分布式锁。

### 12.2 写队列

```go
writeCh chan *Entry  // 容量 1024
workers  = 2         // 固定 2 个，避免 Redis 连接争抢
```

满则丢弃（§6.2）。**绝不阻塞主链路**。丢弃率进 metric，持续 > 5% 说明容量不足或 Redis 变慢，需要告警。

### 12.3 熔断

| 依赖 | 熔断条件 | 恢复 |
|---|---|---|
| Redis | 连续 10 次错误 | 30s 后 half-open，放 1 个请求探测 |
| embedding | 连续 5 次超时 | 60s 内直接返回 miss，不再调用 |
| 判官 | 连续 5 次失败 | 关闭灰区判定 5 分钟，灰区一律按 miss |

熔断状态必须是**进程内原子变量**，不要用 Redis 存（Redis 挂了就查不到熔断状态）。

### 12.4 淘汰

- 全局计数器 `entryCount`（启动时从 `HLEN` 汇总，之后本地增减）。
- 超过 `MaxTotalEntries` 时，按 `:idx` ZSet 的 `CreatedAt` 从旧到新删，一次删 `over * 1.2` 条，避免频繁触发。
- 淘汰是后台任务（每 60s 一次），不在请求路径上。

---

## §13 可观测性

### 13.1 指标

替换现有 `RecordSemanticCache`（`pkg/obs/metrics.go:869-880`），保留旧名字做兼容，新增：

| 指标 | 类型 | 标签 | 用途 |
|---|---|---|---|
| `logos.scache.lookup` | Counter | `decision` | 各分支占比，判断阈值是否合理 |
| `logos.scache.similarity` | Histogram | `decision` | 标定复核，看线上分布是否漂移 |
| `logos.scache.lookup.duration` | Histogram | `stage=exact/slot/embed/search/judge` | 定位慢在哪 |
| `logos.scache.store` | Counter | `result` | 写入成功率与拒绝原因 |
| `logos.scache.entries` | Gauge | `scope=total/hot` | 容量监控 |
| `logos.scache.judge` | Counter | `verdict` | 判官收益与成本 |
| `logos.scache.false_hit_suspect` | Counter | `reason` | 见 13.3 |
| `logos.scache.evict` | Counter | — | 淘汰频率 |

### 13.2 日志字段

每次命中必须结构化输出：

```
event=scache_hit
decision=hit_vector
sim=0.9512
sim_bucket=0.95
scope_hash=ab12...
bot_id=...
user_id=...
entry_id=...
entry_age_s=1830
query_len=12
latency_ms=3
```

`entry_age_s` 很重要：一个频繁命中的条目如果 age 接近 TTL，说明这条知识长期不变，可以延长它的 TTL（后续优化）。

### 13.3 假命中怎么被发现

这是 G2 的落地手段，必须有：

1. **重问检测**：命中后 N 分钟内，同一用户对同一 Bot 再次提出高相似问题（sim ≥ mid），记 `false_hit_suspect{reason=reask}`。
2. **否定反馈**：下一条消息命中 `不是这个 / 不对 / 答非所问 / 重新回答` 词表，记 `{reason=reject}`。
3. **人工抽检**：每周从 `hit_vector` 且 `sim` 在 `[high, high+0.02]` 的样本里抽 50 条，人工判断。这部分数据回流到 §10 的标定集。

指标本身不能证明是假命中，但能让它**可见**。没有这三个信号，缓存就是黑盒。

---

## §14 配置

```go
// config/config.go 新增
type SemanticCache struct {
	Mode               string  `mapstructure:"mode"`                 // off | shadow | on
	MidThreshold       float64 `mapstructure:"mid_threshold"`        // 例 0.88，必须来自标定
	HighThreshold      float64 `mapstructure:"high_threshold"`       // 例 0.94
	MaxEntriesPerScope int     `mapstructure:"max_entries_per_scope"`// 默认 200
	MaxTotalEntries    int     `mapstructure:"max_total_entries"`    // 默认 50000
	EntryTTLHours      int     `mapstructure:"entry_ttl_hours"`      // 默认 24
	ExactTTLHours      int     `mapstructure:"exact_ttl_hours"`      // 默认 24
	JudgeEnabled       bool    `mapstructure:"judge_enabled"`        // 默认 true
	JudgeTimeoutMS     int     `mapstructure:"judge_timeout_ms"`     // 默认 300
	WriteQueueSize     int     `mapstructure:"write_queue_size"`     // 默认 1024
	WriteWorkers       int     `mapstructure:"write_workers"`        // 默认 2
	CalibratedFor      string  `mapstructure:"calibrated_for"`       // "bge-m3/1024"
	ReplayChunkRunes   int     `mapstructure:"replay_chunk_runes"`   // 默认 24
	ReplayBudgetMS     int     `mapstructure:"replay_budget_ms"`     // 默认 400
}
```

```yaml
semantic_cache:
  mode: shadow
  mid_threshold: 0.88
  high_threshold: 0.94
  max_entries_per_scope: 200
  max_total_entries: 50000
  entry_ttl_hours: 24
  judge_enabled: true
  calibrated_for: "bge-m3/1024"
```

**启动校验**（`New` 里做，失败直接返回 error，让进程起不来）：

- `0 < mid < high < 1`
- `mode ∈ {off, shadow, on}`
- `on` 模式下 `CalibratedFor` 不能为空

### 14.1 per-Bot 覆盖

`bot.Config` 增加两个键：

| key | 值 | 说明 |
|---|---|---|
| `enable_semantic_cache` | `"true"` / `"false"` | 未设置时跟随全局 |
| `scache_mode` | `off/shadow/on` | 灰度放量用 |

---

## §15 测试计划

### 15.1 单元测试（`scache/` 包内）

| 名称 | 验证点 |
|---|---|
| `TestNormalize_UnicodeAndPunct` | 全半角、连续空白、句末标点归一 |
| `TestScope_HashIsOrderSensitive` | 字段顺序不同 → 不同 hash |
| `TestScope_DifferentKBVersionMisses` | 改 KBVersion → 必 miss（D1） |
| `TestScope_DifferentPromptHashMisses` | 改 prompt → 必 miss（D1） |
| `TestSlot_NegationVeto` | 开启/关闭 → `Compatible=false`（D4） |
| `TestSlot_NumberVeto` | 500元/1000元 → false |
| `TestSlot_EntityOrderInsensitive` | "北京到上海"/"上海到北京" 实体集合相同 → **注意**：这两个是不同问题，需要靠 `Compare` 或专门的方向槽位处理，测试要断言当前实现会 miss |
| `TestSlot_MissingExtractionPasses` | 一方实体为空 → 放行（防误杀） |
| `TestEntry_ValidRejectsWrongDim` | 维度不符 → 丢弃（D5） |
| `TestLookup_ExactHitSkipsEmbed` | 精确命中时 embed 调用次数 = 0（D11） |
| `TestLookup_EmbedErrorMeansMiss` | 不 panic、不返回 error |
| `TestLookup_ShadowModeDoesNotReturn` | shadow 返回 miss 但记录 `MissShadowed` |
| `TestStore_RejectsDegradedAnswer` | "抱歉，我无法回答" 不入库（D8） |
| `TestStore_RejectsToolTurn` | `ToolCalled=true` 不入库 |
| `TestStore_QueueFullDrops` | 队列满不阻塞、不 panic |
| `TestReplay_ChunkCountAndBudget` | 分块数与总时长上限 |

### 15.2 集成测试

用 `cache.Cache` 接口的**内存实现**（`pkg/cache` 里补一个 `NewMemoryCache()`，测试专用），不引入 miniredis 依赖：

| 名称 | 验证点 |
|---|---|
| `TestIntegration_WriteThenRead` | 写入后同 query 精确命中 |
| `TestIntegration_ParaphraseHitsVector` | 改写后向量命中（依赖固定假 embedder） |
| `TestIntegration_TTLExpiry` | 过期后 miss |
| `TestIntegration_ConcurrentStore` | 100 goroutine 并发写，条目数正确、无 data race（`-race`） |
| `TestIntegration_Eviction` | 超过上限后旧条目被淘汰 |
| `TestIntegration_KBBumpInvalidates` | `BumpKBVersion` 后立即 miss |

**假 embedder**：用确定性哈希把文本映射到固定向量（同义句共享词袋 → 高相似），保证测试不依赖外部 API。放在 `scache/internal/fakeembed`。

### 15.3 标定回归

`cmd/tools/scache-calibrate` 加 `-check` 模式：把当前配置的阈值跑一遍 `calib.jsonl`，若 FHR 超过 1% 则退出码非 0。接进 CI 的 nightly，不阻塞每次提交。

### 15.4 压测

单实例、1 万 scope、每 scope 200 条：

| 场景 | 目标 |
|---|---|
| 纯精确命中 | P99 < 2ms |
| 向量命中（本地） | P99 < 5ms |
| 灰区（含判官 mock） | P99 < 310ms |
| 写入 QPS | 2000/s 不丢队列 |

压测脚本放 `scripts/bench/scache/`，结果写入 PR 描述。**没跑就写"未压测"，不要编数字。**

---

## §16 实施阶段

### P0（半天）—— 止血

只做**不改变架构**的修复：

1. `Store` 加准入（§6.4）。
2. `Store` 增加 `EmbedKey + Dim` 字段与 `Valid` 校验（D5）。
3. 阈值从常量改为配置项，默认值保持 0.90，行为不变。
4. `Lookup` 顺序改为：探活 → 精确命中 → embedding（D11）。
5. 补 `TestEntry_ValidRejectsWrongDim`、`TestStore_RejectsDegradedAnswer`。

验收：`go test ./internal/service/ai/bot/service/...` 全绿，行为无变化。

### P1（1~2 天）—— 正确性

1. 新建 `scache/` 包，落地 §5 数据模型。
2. Scope 全维度 + 版本前缀（§11）。
3. 槽位抽取与门控（§7）。
4. 存储切到 Hash + ZSet，进程内索引（§9.2）。
5. 灰度开关 `off/shadow/on`（§14）。

验收：shadow 模式跑通，指标可见。

### P2（2~3 天）—— 精度

1. 判官（§8）。
2. 标定工具 + 数据集（§10）。
3. 调优出 `mid/high`，写入配置。
4. 假命中信号（§13.3）。

验收：标定集 FHR < 1%，shadow 数据的人工抽检通过。

### P3（3~5 天）—— 稳定性

1. singleflight（§12.1）。
2. 有界写队列 + 熔断（§12.2/12.3）。
3. LRU 淘汰（§12.4）。
4. 伪流式回放（§6.3）。
5. 压测（§15.4）。

验收：§17 全部通过。

---

## §17 验收标准

| 编号 | 标准 | 验证方式 |
|---|---|---|
| A1 | 改 prompt / 换模型 / 知识库更新后 100% 不命中 | 集成测试 |
| A2 | 标定集假命中率 < 1% | `calibrate -check` |
| A3 | 六类硬负样本（否定/实体/数字/时间/指代/跨用户）逐类单独统计，每类假命中 = 0 | 分类报告 |
| A4 | Redis 全挂时主链路 P99 不劣化超过 5% | 故障注入 |
| A5 | 任意依赖故障不产生 5xx | 故障注入 |
| A6 | `-race` 下并发测试无告警 | CI |
| A7 | 内存占用 < 300MB（5 万条目、1024 维） | 压测 |
| A8 | 无 `redis.NewClient`、无硬编码阈值 | `rg` 检查 |
| A9 | shadow 模式下用户可见行为与 `off` 完全一致 | 对比日志 |

---

## §18 禁区

以下做法**一律禁止**，评审时逐条打勾。

| 编号 | 禁区 | 原因 |
|---|---|---|
| X1 | 用 `SCAN` / `KEYS` 遍历删除缓存 | 大 key 阻塞 Redis，生产事故高发 |
| X2 | 缓存工具调用轮 / planner 多步结果 | 副作用不可重放，答案依赖外部状态 |
| X3 | `temperature > 0` 的 Bot 启用缓存 | 答案本身不确定，"相同答案"无意义 |
| X4 | 跨用户共享缓存 | 隐私泄漏，且无法按用户撤回 |
| X5 | 把 embedding 调用放在探活之前 | 白付钱（D11） |
| X6 | 缓存路径返回 error 给调用方 | 缓存是可选优化（I2） |
| X7 | 用 `context.Background()` 但无超时 | goroutine 泄漏 |
| X8 | 新增 `redis.NewClient` | 违背 C4，多一个连接池 |
| X9 | 硬编码阈值 / TTL / 条数 | 违背 C5 |
| X10 | 用字符串包含判断 `Decision` | 用枚举 + 常量 |
| X11 | 在请求路径上做淘汰/清理 | 拉长尾延迟 |
| X12 | 先改 `bot.go` 再接 `scache` | 无法独立测试，回滚困难 |
| X13 | 不写 `reason` 就返回 miss | 线上无法排障（G6） |
| X14 | 用模型生成标定集 | 分布失真，指标失效 |

---

## §19 附录

### 19.1 改造点索引

| file:line | 现状 | 目标 |
|---|---|---|
| `semantic_cache.go:20-26` | 6 个硬编码常量 | 全部迁入 `config.SemanticCache` |
| `semantic_cache.go:37-44` | 自持 `*redis.Client` | 持 `cache.Cache` 接口 |
| `semantic_cache.go:62-67` | 构造函数接收 addr/password/db | 接收 `Config` 结构体 |
| `semantic_cache.go:69-71` | key = prefix:user:bot | `scache.Scope.Hash()` |
| `semantic_cache.go:74-102` | 24h 冷却 | 删除，交给 `pkg/cache` 熔断 |
| `semantic_cache.go:105-160` | List 全量扫 | `scache.Cache.Lookup` |
| `semantic_cache.go:165-169` | LPUSH+LTRIM | `HSET + ZADD + EXPIRE` pipeline |
| `semantic_cache.go:171-213` | 无准入 | 准入 + 队列 + worker |
| `semantic_cache.go:224-240` | 余弦 | 保留，移动到 `scache/simd.go`（可选 SIMD） |
| `semantic_cache.go:267-276` | 裸 goroutine | 有界 channel |
| `bot.go:227-232` | 装配 | 传 `Config`，注册 `BumpKBVersion` |
| `bot.go:929 / 1057` | 非流式读写 | 传 `LookupRequest/StoreRequest` |
| `bot.go:1140 / 1180 / 1220 / 1232` | 流式读写 | 同上 + `replayAsStream` |
| `pkg/obs/metrics.go:869-880` | 仅 op×result | 按 §13.1 扩展 |
| `config/config.go:143-149` | 无 scache 配置 | 新增 `SemanticCache` 段 |
| `bot.go:524` | auto save KB | 调用 `BumpKBVersion` |

### 19.2 术语

| 术语 | 含义 |
|---|---|
| 假命中（false hit） | 缓存返回了、但答案不适用于当前问题 |
| 灰区 | 相似度落在 `[mid, high)`，需要判官裁决 |
| 槽位（slot） | 从 query 抽出的结构化要点，用于硬门控 |
| scope | 决定"能不能复用"的维度组合 |
| shadow | 完整判定但不返回，用于零风险观测 |
| FHR | False Hit Rate，假命中率 |

### 19.3 一句话定位

> 语义缓存的难点不是"相似就返回"，而是**证明不相似的一定不返回**。整套设计的重心因此放在版本维度、槽位硬门控和灰区判官上，阈值只是最后一层兜底。

### 19.4 交接清单

接手后按顺序做，每步单独 commit：

1. 读 §1 并逐条核对代码，确认行号未漂移；有出入先改本文。
2. 做 P0，跑 `go test ./internal/service/ai/bot/service/...`。
3. 做 P1，开 `mode=shadow`，观察 3 天指标。
4. 做 P2，跑标定脚本，把真实数字填进 §10.3 的报告模板。
5. 做 P3，压测，把结果填进 §15.4。
6. 全部通过 §17 后，才允许 `mode=on` 放量。

**任何一步没有真实数据，就在文档里写"未测"，不要填空。** 这份文档的价值在于可信，不在于好看。

---

## §20 实现状态（2026-11-06）

### 20.1 已落地

| 模块 | 状态 | 位置 |
|---|---|---|
| 七维 Scope（tenant/bot/user/KBVersion/prompt/model/embedding）与隔离 | 已实现 | `scache/scope.go` |
| 归一化、PromptHash、SHA-256 精确层 | 已实现 | `scache/normalize.go` |
| 槽位抽取与硬门控（否定/数字/实体/路线方向） | 已实现 | `scache/slots.go` |
| 进程内索引、TTL、容量与字节上限淘汰 | 已实现 | `scache/index.go` |
| 判定流水线（精确 → 探活 → 向量 → 槽位 → 灰区判官） | 已实现 | `scache/cache.go` |
| 灰区判官与七维 Scope 校验 | 已实现 | `scache/judge.go` |
| 伪流式回放（分片节奏 + 预算） | 已实现 | `scache/replay.go` |
| 有界写队列 + 固定 worker | 已实现 | `scache/cache.go` |
| Redis 持久化与不可用降级（进程内继续命中） | 已实现 | `scache/cache.go` |
| KBVersion 失效与 `BumpKBVersion` | 已实现 | `scache/signal.go` |
| 熔断与指标（命中/拒绝原因/判官/写队列） | 已实现 | `scache/breaker.go`、`pkg/obs/metrics.go` |
| Bot 非流式与流式接入（工具轮、planner、注入命中禁止写入） | 已实现 | `bot.go`、`semantic_cache.go` |
| `calibrated_for` 与 embedding 模型绑定，不匹配降级 shadow | 已实现 | `semantic_cache.go` |
| 阈值标定工具与 `-check` 回归 | 已实现 | `cmd/tools/scache-calibrate`、`scache/calibrate` |

关键正确性约束：灰区判官只裁决**最终会被返回的那一条候选**。实现上先按槽位过滤出兼容候选，再取 `compatible[0]` 交给判官，避免"判官批准候选 2、实际返回候选 1"的假命中。

### 20.2 已验证

```text
go build ./...                                       通过
go test ./internal/service/ai/bot/service/...        通过
go test -race ./internal/service/ai/bot/service/scache/...  通过
```

单测覆盖：归一化、Scope 顺序与空 UserID、PromptHash、槽位门控（含路线问句尾巴）、精确命中不调用 embedding、embedding 失败降级、shadow 不返回、兼容候选优先、灰区无判官、准入禁区、TTL、KBVersion 失效、伪流式回放、并发访问、Close 幂等。

### 20.3 尚未完成

| 项 | 状态 | 说明 |
|---|---|---|
| 真实 embedding 标定（mid/high 阈值） | 未执行 | 标定集 `calib.jsonl` 需真实 query，尚未采集；阈值仍是占位值 |
| 假命中率（FHR）评测 | 未执行 | 无真实标定集，不得填数 |
| 压测（QPS/P99/内存） | 未执行 | §15.4 留空 |
| `mode=on` 灰度放量 | 未开启 | 默认 `mode=shadow` |

**代码缺省为 `mode=off`**；当前部署配置（`config/config.yaml`，按仓库约定不入库）设为 `mode=shadow`：完整判定并记录指标，但不返回缓存答案，用户可见行为与 `off` 一致。切换 `mode=on` 前必须完成阈值标定并在配置中填写 `calibrated_for`；没有真实数据就不放量。


