package gateway

// 网关统一错误码。
//
// 之前 WS / TCP 两个接入协议各自散落着 400/401/429/500 这类魔法数字：
// 同一个语义（例如「未认证」）在不同文件里可能写成 401 或 403，客户端要按
// 协议分支做不同处理；新增一个错误类型时也无从判断「有没有被占用」。
//
// 把错误码收敛成一个包的好处：
//   - 取值唯一，客户端只需按数字分支；
//   - 默认文案集中维护，避免「令牌无效」和「令牌已失效」两种说法混用；
//   - 可重试性（429/500）集中判定，客户端重连/重发策略不必再猜。
//
// 数值刻意沿用 HTTP 语义：网关后续若把同一套错误语义暴露到 REST，
// 不需要再做一层映射，也方便运维按常见 HTTP 错误码的心智模型排障。
const (
	// CodeOK 成功（网关目前不用它出现在错误帧，保留给 ACK 扩展）。
	CodeOK int32 = 0
	// CodeBadRequest 请求格式/参数非法，客户端改请求后才可能成功，不应重试。
	CodeBadRequest int32 = 400
	// CodeUnauthorized 未认证或令牌无效/已撤销/已过期，客户端应重新登录后重连。
	CodeUnauthorized int32 = 401
	// CodeForbidden 已认证但无权执行该操作（例如被禁言仍尝试发消息）。
	CodeForbidden int32 = 403
	// CodeNotFound 目标资源不存在（会话/消息已被删除）。
	CodeNotFound int32 = 404
	// CodeTooManyRequests 触发限流，客户端应退避后重试。
	CodeTooManyRequests int32 = 429
	// CodeInternal 网关内部/下游失败（Kafka 不可用、序列化失败等），可重试。
	CodeInternal int32 = 500
)

// codeMessages 是错误码的默认中文文案。
// 只在调用方没有提供更具体文案时使用：具体文案（例如「request_id 过长」）
// 对排障更有价值，默认文案用于兜底，避免出现空消息的错误帧。
var codeMessages = map[int32]string{
	CodeOK:              "成功",
	CodeBadRequest:      "请求参数无效",
	CodeUnauthorized:    "未认证或令牌已失效，请重新登录",
	CodeForbidden:       "无权执行该操作",
	CodeNotFound:        "目标资源不存在",
	CodeTooManyRequests: "请求过于频繁，请稍后重试",
	CodeInternal:        "服务暂时不可用，请稍后重试",
}

// DefaultErrorMessage 返回错误码的默认文案；未知错误码返回通用兜底文案。
func DefaultErrorMessage(code int32) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}

// ErrorMessage 返回最终使用的文案：调用方传了非空具体文案就用它，
// 否则回落到错误码的默认文案。
//
// 这样调用方可以继续写「request_id 过长（最多 64 字节）」这类精确提示，
// 又不会因为漏传文案而给客户端一个空 message。
func ErrorMessage(code int32, message string) string {
	if message != "" {
		return message
	}
	return DefaultErrorMessage(code)
}

// IsRetryable 判断该错误码是否值得客户端稍后重试。
//
// 只有限流与内部错误可重试：400/401/403/404 都是「请求或身份本身有问题」，
// 原样重试只会放大无效流量（例如令牌撤销后仍不断重连）。
func IsRetryable(code int32) bool {
	switch code {
	case CodeTooManyRequests, CodeInternal:
		return true
	default:
		return false
	}
}
