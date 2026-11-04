package types

// QQ 桥接元数据键。
//
// 这些键是 Bridge 与 Chat Service 之间的契约：Bridge 写入，Chat Service 读取。
// 以前两边各写字符串字面量，改一个键名不会编译失败，只会在线上表现为
// 「标记永远读不到」——把键收敛到一处，让契约变更至少能被编译器发现。
const (
	// MetaQQDirectBot 表示 Bridge 的快速路径已经接管这条消息的 Bot 调用。
	// Chat Service 看到它必须跳过慢路径，否则会重复回复。
	MetaQQDirectBot = "qq_direct_bot"

	// MetaQQFastReplied 表示 Bot 回复已经由快速路径直接发到 QQ，
	// 出站订阅不能再发一次。
	MetaQQFastReplied = "qq_fast_replied"

	// MetaQQFastRecovery 标记一条补偿事件：快速路径失败，请 Chat Service
	// 只补跑 Bot 调用，不要重复落库/推送原消息。
	MetaQQFastRecovery = "qq_fast_recovery"

	// MetaQQFastReason 记录快速路径失败原因，便于日志与指标归因。
	MetaQQFastReason = "qq_fast_reason"

	// MetaQQSelfID 是机器人小号自己的 QQ 号（出站发送时需要）。
	MetaQQSelfID = "qq_self_id"

	// MetaQQUserID 是消息对端用户的 QQ 号。
	MetaQQUserID = "qq_user_id"

	// MetaQQGroupID 是 QQ 群号。
	MetaQQGroupID = "qq_group_id"
)
