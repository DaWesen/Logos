// Package downlink 定义网关下行的统一协议。
//
// 历史缺陷：WebSocket 下行是 `{type, data, timestamp}` 信封，TCP 下行直接把 Kafka 里的
// 原始事件（字段平铺、无信封）塞进连接；两端字段名（data / payload）、嵌套层级、
// 事件覆盖范围（TCP 没有 edit/delete/withdraw）都不一致，客户端必须按传输层分叉适配。
//
// 现在 WS 与 TCP 只负责传输，序列化一律走本包：
//   - 信封固定为 `type + request_id? + data + timestamp`；
//   - data 的字段由 events.go 里的类型化结构定义，两端逐字节一致；
//   - 接收方的选择由 routing.go 统一决定，避免两端各写一套兜底逻辑。
package downlink

import (
	"encoding/json"
	"time"
)

// Type 下行消息类型，取值与前端 message.type 一一对应。
type Type string

const (
	TypeConnect      Type = "connect"
	TypeDisconnect   Type = "disconnect"
	TypeHeartbeat    Type = "heartbeat"
	TypeMessage      Type = "message"
	TypeTyping       Type = "typing"
	TypeReadReceipt  Type = "read_receipt"
	TypeWithdraw     Type = "withdraw"
	TypeDelete       Type = "delete"
	TypeEdit         Type = "edit"
	TypeOnlineStatus Type = "online_status"
	TypeError        Type = "error"
	TypeAck          Type = "ack"
)

// Envelope 是所有下行帧的唯一线上结构。
type Envelope struct {
	Type      Type        `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Payload   interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// New 构造不带 request_id 的下行帧（服务端主动推送）。
func New(t Type, payload interface{}) Envelope {
	return Envelope{Type: t, Payload: payload, Timestamp: time.Now().UnixMilli()}
}

// NewRequest 构造带 request_id 的下行帧（对客户端请求的应答）。
func NewRequest(t Type, requestID string, payload interface{}) Envelope {
	return Envelope{Type: t, RequestID: requestID, Payload: payload, Timestamp: time.Now().UnixMilli()}
}

// Marshal 序列化下行帧。
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}
