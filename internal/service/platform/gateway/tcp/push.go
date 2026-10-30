package tcp

import (
	"context"
	"sync/atomic"

	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

// maxConsecutiveDrops 连续丢弃上限，超过后主动断开连接。
// 与 WS 侧同值同语义：偶尔丢一帧（客户端瞬时抖动）可以靠离线同步补齐，
// 但连续几百帧都投递不进去说明客户端已经实质性失联，继续保留只会占着
// fd、Send 缓冲和 presence 名额。
const maxConsecutiveDrops = 100

// slowClientEvictions 因发送缓冲持续满被主动断开的 TCP 连接数（测试与运维依赖）。
var slowClientEvictions int64

// pushToTCP 非阻塞投递下行帧，语义与 websocket.pushToConn 对齐。
//
// 早先 TCP 侧的 sendMessage 在缓冲满时直接 cleanup，问题在于：
//  1. 单帧瞬时拥塞（一次大群广播把 256 容量填满）就会踢掉一个健康连接，
//     而 WS 侧同样的拥塞只是丢帧，两套协议对同一现象的处理不一致；
//  2. cleanup 会立刻注销会话并删 presence，客户端重连后又要重新走认证、
//     重新进入在线广播，代价远高于丢一帧。
//
// 因此这里改为「丢弃 + 连续计数 + 阈值驱逐」：容忍瞬时拥塞，只淘汰真正失联的连接。
func pushToTCP(conn *TCPConnection, data []byte) {
	if conn == nil || conn.Send == nil {
		return
	}

	conn.mu.Lock()
	closed := conn.IsClosed
	conn.mu.Unlock()
	if closed {
		return
	}

	select {
	case conn.Send <- data:
		atomic.StoreInt32(&conn.consecutiveDrops, 0)
	default:
		obs.RecordGatewayDrop(context.Background(), "tcp", "buffer_full")
		n := atomic.AddInt32(&conn.consecutiveDrops, 1)
		if n == 1 || n%50 == 0 {
			logger.Warn("TCP 发送通道已满，丢弃下行帧",
				logger.StringField("session_id", conn.SessionID),
				logger.IntField("consecutive_drops", int(n)))
		}
		if n >= maxConsecutiveDrops {
			logger.Warn("TCP 慢客户端连续丢弃过多，主动断开",
				logger.StringField("session_id", conn.SessionID),
				logger.StringField("user_id", conn.UserID))
			atomic.AddInt64(&slowClientEvictions, 1)
			// 关闭来源记为 slow；writePump 退出后 cleanup 会据此打点。
			conn.setCloseEvent("slow")
			if conn.Conn != nil {
				_ = conn.Conn.Close()
			}
		}
	}
}
