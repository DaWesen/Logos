package grpcserver

import (
	"os"
	"testing"
	"time"
)

func TestShutdownGracePeriod_Default(t *testing.T) {
	t.Setenv("GRPC_SHUTDOWN_GRACE", "")
	if got := shutdownGracePeriod(); got != 25*time.Second {
		t.Errorf("default grace period = %v, want 25s", got)
	}
}

func TestShutdownGracePeriod_EnvOverride(t *testing.T) {
	t.Setenv("GRPC_SHUTDOWN_GRACE", "60")
	if got := shutdownGracePeriod(); got != 60*time.Second {
		t.Errorf("override grace period = %v, want 60s", got)
	}
}

func TestShutdownGracePeriod_InvalidEnv(t *testing.T) {
	for _, v := range []string{"abc", "-5", "0"} {
		t.Setenv("GRPC_SHUTDOWN_GRACE", v)
		if got := shutdownGracePeriod(); got != 25*time.Second {
			t.Errorf("invalid %q should fall back to default, got %v", v, got)
		}
	}
}

func TestDefaultGraceLessThanK8sDefault(t *testing.T) {
	// 宽限期必须小于 K8s 默认 terminationGracePeriodSeconds(30s)，
	// 保证进程能在被 SIGKILL 前自行完成排空
	if defaultShutdownGrace >= 30*time.Second {
		t.Errorf("default grace %v must be < 30s (K8s default)", defaultShutdownGrace)
	}
}

// 编译期保证 StartServer 可用性不受环境变量缺失影响（无 env 时也可运行）
func TestMain(m *testing.M) {
	_ = os.Unsetenv("GRPC_SHUTDOWN_GRACE")
	os.Exit(m.Run())
}
