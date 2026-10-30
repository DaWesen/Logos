package gateway

import (
	"unicode"
)

// 上行帧公共校验上限。
//
// WS 与 TCP 是两套传输协议，但上行语义完全相同（request_id、chat_id、内容、
// 已读消息 ID）。此前两边的校验各写一份，且规则逐渐漂移：
// WS 限制 request_id <= 64、TCP 相同；WS 校验了 content 非空、TCP 把
// chat_id 与 content 合成一条报错；已读回执在 TCP 侧完全没校验消息 ID 列表。
//
// 规则漂移的后果不只是「两边行为不一致」：任一入口漏校验，就等于给另一条
// 协议留了一个绕过入口。把这些规则收敛到一个包里，两边都调用同一批函数，
// 才有「同一语义只存在一份实现」。
const (
	// MaxRequestIDBytes request_id 上限。
	// request_id 会作为幂等键落库（varchar(64)），超长会在消费端写入失败并触发
	// Kafka 重投，所以必须在网关入口拒绝，而不是等数据库报错。
	MaxRequestIDBytes = 64
	// MaxChatIDBytes 会话 ID 上限。正常会话 ID 是 UUID/雪花 ID（< 40 字节），
	// 128 字节足够容纳任何合法实现，同时挡住把整段 JSON 塞进 chat_id 的滥发。
	MaxChatIDBytes = 128
	// MaxMessageIDBytes 单条消息 ID 上限，规则与 request_id 一致。
	MaxMessageIDBytes = 64
	// MaxContentBytes 单条消息内容上限（64 KiB，按字节而非字符）。
	// 中文在 UTF-8 下约 3 字节/字，64 KiB 仍可容纳 2 万字以上的长文；
	// 更大的内容应走文件/富媒体上传，而不是消息帧。
	MaxContentBytes = 64 << 10
	// MaxMessageIDsPerReceipt 单次已读回执最多携带的消息数。
	// 前端通常按会话批量标记已读，100 条足够；更大的列表说明客户端在滥用
	// 「一次回执标记全历史」，会放大 Kafka 消息体与下游写放大。
	MaxMessageIDsPerReceipt = 100
	// MaxMentionIDs 单条消息最多 @ 的人数，避免用户名列表无界增长。
	MaxMentionIDs = 50
)

// Violation 描述一次上行校验失败。
// 携带字段名与具体原因，调用方可以直接拼进错误帧，日志里也能按字段聚合。
type Violation struct {
	Field  string
	Reason string
}

func (v *Violation) Error() string {
	if v == nil {
		return ""
	}
	return v.Field + ": " + v.Reason
}

// ValidateRequestID 校验幂等键的长度与字符集。
//
// 除了长度，还拒绝空白与控制字符：request_id 会进入日志、Redis key 与数据库，
// 带换行/制表符的值会让日志被伪造（日志注入），也会让按前缀扫描的 key 失效。
func ValidateRequestID(requestID string) *Violation {
	if len(requestID) > MaxRequestIDBytes {
		return &Violation{Field: "request_id", Reason: "长度超过 64 字节"}
	}
	if hasControlOrSpace(requestID) {
		return &Violation{Field: "request_id", Reason: "包含空白或控制字符"}
	}
	return nil
}

// ValidateChatID 校验会话 ID：非空、有长度上限、不含空白/控制字符。
func ValidateChatID(chatID string) *Violation {
	if chatID == "" {
		return &Violation{Field: "chat_id", Reason: "不能为空"}
	}
	if len(chatID) > MaxChatIDBytes {
		return &Violation{Field: "chat_id", Reason: "长度超过 128 字节"}
	}
	if hasControlOrSpace(chatID) {
		return &Violation{Field: "chat_id", Reason: "包含空白或控制字符"}
	}
	return nil
}

// ValidateContent 校验消息正文：非空且不超过 MaxContentBytes。
// 只按字节数限制，不做字符集校验：正文允许任意 Unicode（表情、RTL 文本等），
// 字符集校验容易误伤正常用户，而长度上限已经足以阻止内存放大。
func ValidateContent(content string) *Violation {
	if content == "" {
		return &Violation{Field: "content", Reason: "不能为空"}
	}
	if len(content) > MaxContentBytes {
		return &Violation{Field: "content", Reason: "长度超过 64 KiB"}
	}
	return nil
}

// ValidateMessageIDs 校验已读回执/批量操作携带的消息 ID 列表。
//
// 空列表、超过数量上限、单个 ID 为空或超长都拒绝：
// 空列表没有业务含义；单个空 ID 会让下游按空主键查询；
// 超长 ID 同样会进入 SQL/日志。
func ValidateMessageIDs(ids []string) *Violation {
	if len(ids) == 0 {
		return &Violation{Field: "message_ids", Reason: "不能为空"}
	}
	if len(ids) > MaxMessageIDsPerReceipt {
		return &Violation{Field: "message_ids", Reason: "单次最多 100 条"}
	}
	for _, id := range ids {
		if id == "" {
			return &Violation{Field: "message_ids", Reason: "存在空消息 ID"}
		}
		if len(id) > MaxMessageIDBytes {
			return &Violation{Field: "message_ids", Reason: "消息 ID 长度超过 64 字节"}
		}
		if hasControlOrSpace(id) {
			return &Violation{Field: "message_ids", Reason: "消息 ID 包含空白或控制字符"}
		}
	}
	return nil
}

// ValidateMentionIDs 校验 @ 用户列表：数量上限 + 单个 ID 非空/不超长。
// @ 只是通知提示，非法 ID 不应导致整条消息失败，因此这里只拒绝明显滥用的输入；
// 调用方按需选择是否「过滤后继续」。
func ValidateMentionIDs(ids []string) *Violation {
	if len(ids) > MaxMentionIDs {
		return &Violation{Field: "mention_user_ids", Reason: "单条消息最多 @ 50 人"}
	}
	for _, id := range ids {
		if id == "" {
			return &Violation{Field: "mention_user_ids", Reason: "存在空用户 ID"}
		}
		if len(id) > MaxChatIDBytes {
			return &Violation{Field: "mention_user_ids", Reason: "用户 ID 长度超过 128 字节"}
		}
	}
	return nil
}

// hasControlOrSpace 判断字符串是否包含空白或控制字符。
// 用 unicode.IsSpace/IsControl 而不是只查 ASCII：全角空格、零宽字符同样会
// 破坏日志与 key 的可读性，且都是常见绕过手段。
func hasControlOrSpace(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}
