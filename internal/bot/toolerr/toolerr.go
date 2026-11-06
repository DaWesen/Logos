// Package toolerr 定义工具执行层与规划器共享的错误契约。
//
// 重试策略必须建立在错误类型上，而不是错误文案上：上游多包一层、
// 或改一个字的提示语，字符串匹配就会悄悄失效，最终表现为
// 「偶发不重试」这种极难复现的问题。把哨兵错误放在独立包里，
// 是为了让工具层与规划器复用同一套判定，同时避免两边互相 import。
package toolerr

import (
	"context"
	"errors"
)

var (
	// ErrToolNotFound 工具不存在或未注册，重试没有意义。
	ErrToolNotFound = errors.New("tool not found")
	// ErrInvalidArgs 工具参数非法，重试没有意义。
	ErrInvalidArgs = errors.New("invalid tool arguments")
	// ErrPermission 权限不足，重试没有意义。
	ErrPermission = errors.New("permission denied")
	// ErrRateLimited 上游限流，属于可重试的瞬时失败。
	ErrRateLimited = errors.New("rate limited")
	// ErrTransient 供工具层包装超时、5xx、连接重置等瞬时错误。
	ErrTransient = errors.New("transient failure")
)

// Kind 只区分对重试策略有意义的两类失败。
//
// 未知错误默认归为永久失败：盲目重试会放大副作用与成本，
// 想获得重试能力必须显式包装 ErrTransient / ErrRateLimited。
type Kind int

const (
	// KindPermanent 重试没有意义，或无法证明重试安全。
	KindPermanent Kind = iota
	// KindTransient 瞬时失败，可按退避策略重试。
	KindTransient
)

// Classify 把错误映射为失败等级。
//
// 调用方主动取消不重试：用户已经离开，继续重试只会白烧一次上游调用。
// 超时与 429/5xx 归为瞬时失败；参数、权限、工具不存在归为永久失败。
func Classify(err error) Kind {
	if err == nil {
		return KindPermanent
	}
	switch {
	case errors.Is(err, context.Canceled):
		return KindPermanent
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrTransient),
		errors.Is(err, context.DeadlineExceeded):
		return KindTransient
	case errors.Is(err, ErrToolNotFound), errors.Is(err, ErrInvalidArgs),
		errors.Is(err, ErrPermission):
		return KindPermanent
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return KindTransient
	}
	return KindPermanent
}

// IsTransient 判断错误是否值得按退避策略重试。
func IsTransient(err error) bool { return Classify(err) == KindTransient }

// IsPermanent 判断错误是否已经明确不可重试。
//
// 注意它只回答「是否明确永久」；未知错误也会返回 true，
// 调用方如果希望保留「未知即重试」的旧行为，应只对已知的
// ErrToolNotFound / ErrInvalidArgs / ErrPermission 做短路判断。
func IsPermanent(err error) bool { return Classify(err) == KindPermanent }
