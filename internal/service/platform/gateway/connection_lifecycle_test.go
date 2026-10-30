package gateway

import "testing"

// TestConnectionLifecycleInvariants 把「心跳/读超时/presence 过期」之间的关系钉成测试。
// 这几个常量一旦被单独调大或调小，线上表现是「健康连接被判离线」或
// 「宕机节点上的用户永远在线」，但任何单测都不会失败——所以必须显式断言。
func TestConnectionLifecycleInvariants(t *testing.T) {
	if WSPingInterval >= WSReadTimeout {
		t.Fatalf("WS ping 周期(%s)必须小于读超时(%s)，否则 pong 来不及在读截止前返回",
			WSPingInterval, WSReadTimeout)
	}
	if WSReadTimeout >= PresenceStaleAfter {
		t.Fatalf("WS 读超时(%s)必须小于 presence 过期窗口(%s)，否则健康连接会被判离线",
			WSReadTimeout, PresenceStaleAfter)
	}
	if TCPReadTimeout >= PresenceStaleAfter {
		t.Fatalf("TCP 读超时(%s)必须小于 presence 过期窗口(%s)，否则健康连接会被判离线",
			TCPReadTimeout, PresenceStaleAfter)
	}
	// 抖动后的最大 ping 周期仍必须小于读超时：否则最“慢”的那批连接
	// 会在 pong 返回前就触发读超时，抖动反而制造了随机掉线。
	if jitteredUpper := JitterDuration(WSPingInterval, PingJitterFraction, 1); jitteredUpper >= WSReadTimeout {
		t.Fatalf("抖动后 ping 上界(%s)必须小于读超时(%s)", jitteredUpper, WSReadTimeout)
	}
	// 抖动下界也不能过低：过短周期会让弱网客户端被高频 ping 打扰。
	if jitteredLower := JitterDuration(WSPingInterval, PingJitterFraction, 0); jitteredLower <= 0 {
		t.Fatalf("抖动下界(%s)必须为正", jitteredLower)
	}
	if WSAuthTimeout >= WSReadTimeout {
		t.Fatalf("未认证超时(%s)应短于正常读超时(%s)，避免匿名连接长期占用 fd",
			WSAuthTimeout, WSReadTimeout)
	}
}
