package tcp

import "time"

// TCP Accept 连续失败后的重试退避参数。
//
// Accept 返回的错误不全是「连接已关闭」：fd 耗尽（EMFILE/ENFILE）、临时端口耗尽、
// listener 被内核短暂拒绝等都属于可恢复错误。原实现直接 `continue`，在 fd 耗尽时
// 会变成零间隔的忙等循环——CPU 打满、日志刷屏，反而让释放 fd 的路径更难推进。
const (
	acceptInitialBackoff = 50 * time.Millisecond
	acceptMaxBackoff     = time.Second
)

// NextAcceptBackoff 计算下一次 Accept 失败后的退避时间：
// 首次失败从 50ms 开始，之后逐次翻倍，封顶 1s；current<=0 视为首次失败。
// 成功接受一个连接后调用方应传 0 重置，避免「偶发失败」把后续正常路径也拖慢。
func NextAcceptBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return acceptInitialBackoff
	}
	next := current * 2
	if next > acceptMaxBackoff {
		return acceptMaxBackoff
	}
	return next
}
