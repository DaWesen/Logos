package governance

import "strings"

// RetryPolicy 决定哪些 RPC 允许自动重试。
//
// 为什么不能「所有方法都重试」：
// gRPC 的 Unavailable 可能发生在「请求已到达服务端、但响应丢在路上」，
// 服务端可能已经执行了副作用。对 SendMessage / Deposit / ConsumeModelCall
// 这类非幂等写操作重试，会变成重复发消息、重复充值、重复扣费。
//
// 这里的策略是**白名单**而不是黑名单：
//   - 默认不重试，新增的写接口天然安全；
//   - 只对明确只读或已确认幂等的方法开启；
//   - 黑名单的问题是「忘了加一个方法」就直接造成资损，白名单的失败模式
//     只是「少重试一次」，可以接受。
type RetryPolicy struct {
	// RetryAll 仅用于测试或特殊网关；生产默认 false。
	RetryAll bool
	// ExtraSafeMethods 追加允许重试的方法（完整路径或方法名均可）。
	ExtraSafeMethods []string
}

// safeRetryMethods 是明确可安全重试的方法名（小写比较）。
//
// 判据：
//   - Get/List/Search/Query/Check/Verify 等只读操作；
//   - 明确带幂等键或「重复执行结果一致」的更新（如 UpdateAvatar 覆盖写）。
//
// 刻意排除：SendMessage、SendBotMessage、CreatePaymentOrder、ConfirmPayment、
// Deposit、Withdraw、Consume*、Refund、AddFriend、Create*/Delete* 等。
var safeRetryMethods = map[string]struct{}{
	// 只读查询
	"getuserinfo":            {},
	"getuserinfobyusername":  {},
	"batchgetuserinfo":       {},
	"getuserstats":           {},
	"searchusers":            {},
	"checkusername":          {},
	"batchcheckusernames":    {},
	"verifytoken":            {},
	"getpaymentorder":        {},
	"listpaymentorders":      {},
	"getaccount":             {},
	"gettransactions":        {},
	"getusagestats":          {},
	"querymetrics":           {},
	"querylogs":              {},
	"queryalerts":            {},
	"getservicestatus":       {},
	"listservicestatus":      {},
	"getbot":                 {},
	"listbots":               {},
	"getbothistory":          {},
	"listconversations":      {},
	"getusermemory":          {},
	"getprompt":              {},
	"listprompts":            {},
	"searchmessages":         {},
	"getmessagehistory":      {},
	"getfriendrequests":      {},
	"getgroupmembers":        {},
	"getgroupmember":         {},
	"getgroup":               {},
	"getconversationlist":    {},
	"getunreadcounts":        {},
	"gettotalunreadcount":    {},
	"getmessagereadreceipts": {},
	// 幂等覆盖写（重复执行结果一致）
	"updateavatar":  {},
	"updateuser":    {},
	"setusermemory": {},
}

// ShouldRetry 判断某个 gRPC 方法是否允许自动重试。
// method 形如 "/logos.chat.ChatService/SendMessage"。
func (p RetryPolicy) ShouldRetry(method string) bool {
	if p.RetryAll {
		return true
	}

	name := normalizeMethodName(method)
	if name == "" {
		return false
	}
	if _, ok := safeRetryMethods[name]; ok {
		return true
	}
	for _, extra := range p.ExtraSafeMethods {
		if normalizeMethodName(extra) == name {
			return true
		}
	}
	return false
}

// normalizeMethodName 取最后一段并去掉 Service 前缀与大小写差异。
// 兼容三种写法："/pkg.Svc/Method"、"pkg.Svc/Method"、"Method"。
func normalizeMethodName(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return ""
	}
	if idx := strings.LastIndex(method, "/"); idx >= 0 {
		method = method[idx+1:]
	}
	if idx := strings.LastIndex(method, "."); idx >= 0 {
		method = method[idx+1:]
	}
	return strings.ToLower(method)
}
