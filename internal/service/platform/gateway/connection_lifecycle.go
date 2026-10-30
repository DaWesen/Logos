package gateway

import "time"

// 网关连接存活参数。
//
// 这些值必须成组调整：WS 的 ping 周期、读超时、TCP 读超时和 presence 过期窗口
// 共同决定「连接是否被认为存活」。历史上它们是散落在 websocket/tcp/presence 三处的
// 魔法数字，单独调大或调小其中一项都会引入两类线上问题：
//   - presence 过期窗口 < ping/读超时：健康连接会被短暂判定离线，触发伪造的
//     offline→online 抖动，并让所有会话收到一次无效的在线状态广播；
//   - presence 过期窗口远大于读超时：节点宕机后用户长时间维持「在线」假象，
//     消息投递到已死亡的连接上。
//
// 不变式（connection_lifecycle_test.go 会断言）：
//
//	WSPingInterval < WSReadTimeout < PresenceStaleAfter
//	TCPReadTimeout < PresenceStaleAfter
//
// 即每次 ping/心跳都必须在读超时之前到达，而读超时必须短于 presence 过期窗口。
const (
	// WSPingInterval WS 服务端 ping 周期；客户端 pong 与上行消息都会续期。
	WSPingInterval = 60 * time.Second
	// WSReadTimeout WS 单次读超时。取值大于 WSPingInterval，给 pong 留出网络裕量，
	// 否则 ping 恰好落在读截止点附近时会出现「ping 已发出、pong 还没读回就超时」的竞态。
	WSReadTimeout = 90 * time.Second
	// WSAuthTimeout 未认证连接允许的最长存活时间，超时即断开，避免匿名连接长期占用 fd。
	WSAuthTimeout = 10 * time.Second
	// WSWriteTimeout 单次下行写入的超时；写满一个卡死的连接不能永久挂住 writePump。
	WSWriteTimeout = 10 * time.Second
	// TCPReadTimeout TCP 单次读超时；任何上行帧都会续期。
	TCPReadTimeout = 120 * time.Second
	// TCPWriteTimeout 单次下行写入的超时。
	TCPWriteTimeout = 10 * time.Second
	// PresenceStaleAfter 会话超过该时长没有续期即视为失效。
	// 必须大于 WSReadTimeout 与 TCPReadTimeout，保证健康连接一定能在失效前续期；
	// 节点宕机遗留的会话最迟在该窗口后自动剔除。
	PresenceStaleAfter = 3 * time.Minute
)
