package logger

import "testing"

// 没有配置文件时 logger 必须退回默认值，而不是 panic。
// 任何单元测试只要调用一次 logger.Warn，都会走到这条路径。
func TestGetLogger_WithoutConfigUsesDefaults(t *testing.T) {
	if l := GetLogger(); l == nil {
		t.Fatal("无配置时 GetLogger 仍应返回可用实例")
	}
	Warn("no config smoke test")
}
