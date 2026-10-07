package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"Logos/pkg/logger"
)

// 跨节点广播的目标模式
const (
	TargetModeUser      = "user"      // 投递给某个用户的所有会话
	TargetModeSession   = "session"   // 投递给某个会话
	TargetModeUsers     = "users"     // 投递给多个用户
	TargetModeBroadcast = "broadcast" // 广播给所有连接（可选排除某用户）
)

// BroadcastMessage 跨节点广播消息体
type BroadcastMessage struct {
	SourceNodeID    string   `json:"source_node_id"`              // 发送节点 ID，用于回环过滤
	TargetMode      string   `json:"target_mode"`                  // 见 TargetMode* 常量
	TargetUserID    string   `json:"target_user_id,omitempty"`     // TargetMode = user / session
	TargetSessionID string   `json:"target_session_id,omitempty"`   // TargetMode = session
	TargetUserIDs   []string `json:"target_user_ids,omitempty"`     // TargetMode = users
	ExceptUserID    string   `json:"except_user_id,omitempty"`      // TargetMode = broadcast
	Payload         []byte   `json:"payload"`                       // 原始消息字节，不在 broadcaster 层反序列化
}

// MessageHandler 远端消息处理回调，由 UnifiedConnectionManager 注入
type MessageHandler func(msg *BroadcastMessage)

// Broadcaster 跨节点 WebSocket 消息广播器，基于 Redis PubSub
//
// 设计要点：
//  1. 单 channel 复用：所有节点订阅 `logos:ws:broadcast`，消息体内 TargetMode 区分目标
//     不按用户分 channel，避免 N 倍订阅开销
//  2. 回环过滤：Subscribe handler 首判 SourceNodeID == selfNodeID 跳过，防止节点收到自己 publish 的消息
//  3. 共享连接池：复用 cache.RedisCache 底层 *redis.Client，不新建连接
//  4. nil 安全：broadcaster 为 nil 时 UnifiedConnectionManager 退化为纯本地投递
type Broadcaster struct {
	nodeID  string
	channel string
	rdb     *redis.Client

	mu      sync.Mutex
	pubsub  *redis.PubSub
	closed  bool
}

// NewBroadcaster 构造 Broadcaster
//
//	rdb       - 复用的 redis client（来自 cache.RedisCache.RawClient()）
//	nodeID    - 节点 ID（来自 cfg.GetGatewayNodeID()）
//	channel   - PubSub channel 名（来自 cfg.GetGatewayPubSubChannel()）
func NewBroadcaster(rdb *redis.Client, nodeID, channel string) *Broadcaster {
	return &Broadcaster{
		nodeID:  nodeID,
		channel: channel,
		rdb:     rdb,
	}
}

// NodeID 返回节点 ID（供 UnifiedConnectionManager 构造消息时使用）
func (b *Broadcaster) NodeID() string {
	return b.nodeID
}

// Channel 返回 PubSub channel 名
func (b *Broadcaster) Channel() string {
	return b.channel
}

// Publish 发布消息到 PubSub channel
// 仅序列化 + Publish，不做本地投递（本地投递由 UnifiedConnectionManager 负责）
func (b *Broadcaster) Publish(ctx context.Context, msg *BroadcastMessage) error {
	if b == nil || b.rdb == nil {
		return nil
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("broadcaster marshal: %w", err)
	}
	if err := b.rdb.Publish(ctx, b.channel, payload).Err(); err != nil {
		return fmt.Errorf("broadcaster publish: %w", err)
	}
	return nil
}

// Subscribe 订阅 PubSub channel，收到远端消息后调用 handler
//
// 阻塞调用，应在 goroutine 中启动。内部带退避重连：当订阅通道被关闭或
// redis 抖动断开时，会等待后重新订阅，直到 ctx 取消或 Close 调用。
// handler 在订阅 goroutine 内同步执行，应快速返回（仅做本地投递，IO 走 channel buffer）。
func (b *Broadcaster) Subscribe(ctx context.Context, handler MessageHandler) error {
	if b == nil || b.rdb == nil {
		<-ctx.Done()
		return nil
	}

	const (
		initialBackoff = time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff

	for {
		start := time.Now()
		err := b.subscribeOnce(ctx, handler)
		if ctx.Err() != nil {
			return nil
		}

		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return err
		}

		// 订阅稳定运行较长时间后再断开，则重置退避，避免长连接偶发抖动后仍长时间间隔
		if time.Since(start) > maxBackoff {
			backoff = initialBackoff
		}

		logger.Warn("broadcaster: subscribe interrupted, will reconnect",
			logger.ErrorField(err),
			logger.StringField("channel", b.channel),
			logger.StringField("backoff", backoff.String()))

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// subscribeOnce 执行一次订阅，直到 ctx 取消、Close 调用或下游 channel 关闭
func (b *Broadcaster) subscribeOnce(ctx context.Context, handler MessageHandler) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return fmt.Errorf("broadcaster already closed")
	}
	b.pubsub = b.rdb.Subscribe(ctx, b.channel)
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		if b.pubsub != nil {
			_ = b.pubsub.Close()
			b.pubsub = nil
		}
		b.mu.Unlock()
	}()

	ch := b.pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var bm BroadcastMessage
			if err := json.Unmarshal([]byte(msg.Payload), &bm); err != nil {
				logger.Warn("broadcaster unmarshal failed",
					logger.ErrorField(err),
					logger.StringField("channel", b.channel))
				continue
			}
			// 回环过滤：跳过自己 publish 的消息
			if bm.SourceNodeID == b.nodeID {
				continue
			}
			handler(&bm)
		}
	}
}

// Close 关闭订阅（不关闭底层 redis client，由 cache.RedisCache 统一管理）
func (b *Broadcaster) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.pubsub != nil {
		err := b.pubsub.Close()
		b.pubsub = nil
		return err
	}
	return nil
}
