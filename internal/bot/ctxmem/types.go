// Package ctxmem 负责对话上下文的预算推导、分层配额与装配原语。
//
// 这里刻意不依赖 bot 的存储模型：调用方把消息转成 Message/Slot 再传进来，
// 既避免 bot/ctxmem 反向依赖 service 层，也让预算和装配逻辑可以脱离 DB 单测。
package ctxmem

// Layer 上下文的一层。顺序即注入优先级。
type Layer int

const (
	LayerWorking   Layer = iota // 最近若干轮原文
	LayerEpisodic               // 滚动摘要
	LayerSemantic               // 结构化事实槽
	LayerRetrieval              // 按需召回的历史原文
	layerCount
)

func (l Layer) String() string {
	switch l {
	case LayerWorking:
		return "working"
	case LayerEpisodic:
		return "episodic"
	case LayerSemantic:
		return "semantic"
	case LayerRetrieval:
		return "retrieval"
	default:
		return "unknown"
	}
}

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

// Sum 返回各层配额之和。正常情况下应等于 Total。
func (b Budget) Sum() int {
	return b.Working + b.Episodic + b.Semantic + b.Retrieval
}

// Fragment 注入模型的片段，带来源与保真等级。
type Fragment struct {
	Layer   Layer
	Role    string // system / user / assistant
	Content string
	Tokens  int
	// SourceMsgID 溯源：来自哪条消息、哪一轮。空表示合成内容（如摘要）。
	SourceMsgID string
	TurnIndex   int
	// Fidelity 保真等级：原样 / 改写 / 概括。用于注入时的措辞与校验。
	Fidelity Fidelity
}

// Fidelity 保真等级。
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

// LayerQuota 一层的配额声明。
// Min 保证功能不退化，Max 防止单层独占，Demand 给出本次实际需要多少 token。
type LayerQuota struct {
	Min    int
	Max    int
	Demand func() int
}
