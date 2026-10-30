package push

import "strings"

// PushFailureKind 描述一次推送失败的「可重试性」。
//
// 之所以要分类而不是直接把 error 丢给调用方：
//
//   - Retryable：网络抖动、供应商限流、5xx。重试有意义，凭据保留。
//   - Permanent：请求本身或设备令牌已经确定无效（如 APNs 410 / FCM NotRegistered）。
//     继续重试只会把失效令牌反复打给供应商（部分供应商会把持续失败计入配额），
//     并且永远不可能成功——这类失败必须触发令牌注销。
//   - Unknown：无法判断（鉴权配置错误、未知状态码）。此时既不能丢令牌
//     （可能只是配置写错了），也不能无限重试，交给上层告警与人工判断。
//
// 早期实现把三种情况都压成「Success=false + Error 字符串」：失效令牌永不清理，
// 用户换机后旧设备 token 一直留在内存里，每次离线推送都要为一个死 token 打一次
// 外部 HTTP 请求，多设备用户的推送延迟随之线性上升。
type PushFailureKind string

const (
	PushFailureRetryable PushFailureKind = "retryable"
	PushFailurePermanent PushFailureKind = "permanent"
	PushFailureUnknown   PushFailureKind = "unknown"
)

// PushFailure 是一次失败的结构化描述。
type PushFailure struct {
	Kind PushFailureKind
	// Reason 供应商返回的错误原因（APNs 的 reason、FCM 的 results[].error），
	// 为空时回退为状态描述。仅用于日志与指标，不要把它当业务错误码使用。
	Reason string
	// TokenInvalid 表示「设备令牌确定已失效」，调用方应注销该 token。
	// 与 Permanent 分开：PayloadTooLarge 这类永久失败重试无意义，但令牌仍然有效，
	// 注销它会把一个能用的设备踢掉。
	TokenInvalid bool
}

func (f PushFailure) ShouldUnregister() bool {
	return f.Kind == PushFailurePermanent && f.TokenInvalid
}

// 供应商返回的「设备令牌已失效」原因集合。
//
// APNs: device token 被系统回收（卸载重装、恢复备份到新设备）
// FCM: 注册 ID 失效或归属的 sender 不匹配（项目迁移）
var (
	apnsInvalidTokenReasons = map[string]struct{}{
		"BadDeviceToken":         {},
		"Unregistered":           {},
		"DeviceTokenNotForTopic": {},
	}
	fcmInvalidTokenReasons = map[string]struct{}{
		"NotRegistered":       {},
		"InvalidRegistration": {},
		"MismatchSenderId":    {},
	}
	apnsRetryableReasons = map[string]struct{}{
		"ServiceUnavailable":          {},
		"InternalServerError":         {},
		"TooManyProviderTokenUpdates": {},
		"TooManyRequests":             {},
	}
	fcmRetryableReasons = map[string]struct{}{
		"Unavailable":               {},
		"InternalServerError":       {},
		"DeviceMessageRateExceeded": {},
	}
)

// ClassifyPushFailure 依据平台、HTTP 状态码与供应商原因判定失败类型。
//
// 状态码语义优先于 reason：APNs 429/5xx 即使 reason 陌生也一定可重试；
// 反过来，410 是「设备令牌已注销」的权威信号，reason 缺失也应注销。
// 4xx 中除 429 外一律视为请求级永久失败，但不注销令牌（坏的是请求不是设备）。
func ClassifyPushFailure(platform Platform, statusCode int, reason string) PushFailure {
	reason = strings.TrimSpace(reason)

	switch platform {
	case PlatformIOS, PlatformDesktop:
		return classifyAPNS(statusCode, reason)
	case PlatformAndroid:
		return classifyFCM(statusCode, reason)
	default:
		// Web Push / 未知平台没有统一的失效语义，交给上层判断
		if statusCode >= 500 || statusCode == 429 {
			return PushFailure{Kind: PushFailureRetryable, Reason: reason}
		}
		return PushFailure{Kind: PushFailureUnknown, Reason: reason}
	}
}

func classifyAPNS(statusCode int, reason string) PushFailure {
	if statusCode == 410 {
		return PushFailure{Kind: PushFailurePermanent, Reason: reason, TokenInvalid: true}
	}
	if _, ok := apnsInvalidTokenReasons[reason]; ok {
		return PushFailure{Kind: PushFailurePermanent, Reason: reason, TokenInvalid: true}
	}
	if statusCode == 429 || statusCode >= 500 {
		return PushFailure{Kind: PushFailureRetryable, Reason: reason}
	}
	if _, ok := apnsRetryableReasons[reason]; ok {
		return PushFailure{Kind: PushFailureRetryable, Reason: reason}
	}
	if statusCode >= 400 && statusCode < 500 {
		// BadCollapseId / PayloadTooLarge / MissingTopic 等：请求有问题，令牌没问题
		return PushFailure{Kind: PushFailurePermanent, Reason: reason}
	}
	return PushFailure{Kind: PushFailureUnknown, Reason: reason}
}

func classifyFCM(statusCode int, reason string) PushFailure {
	if _, ok := fcmInvalidTokenReasons[reason]; ok {
		return PushFailure{Kind: PushFailurePermanent, Reason: reason, TokenInvalid: true}
	}
	if _, ok := fcmRetryableReasons[reason]; ok {
		return PushFailure{Kind: PushFailureRetryable, Reason: reason}
	}
	// 401/403 是服务端凭据/配置问题，设备令牌本身可能完全正常
	if statusCode == 401 || statusCode == 403 {
		return PushFailure{Kind: PushFailureUnknown, Reason: reason}
	}
	if statusCode == 429 || statusCode >= 500 {
		return PushFailure{Kind: PushFailureRetryable, Reason: reason}
	}
	if statusCode >= 400 {
		return PushFailure{Kind: PushFailurePermanent, Reason: reason}
	}
	return PushFailure{Kind: PushFailureUnknown, Reason: reason}
}
