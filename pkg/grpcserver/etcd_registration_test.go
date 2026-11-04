package grpcserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeRegistration struct {
	mu          sync.Mutex
	registerN   int
	deregisterN int
	closeN      int
	registerErr error
	deregErr    error
	published   bool
}

func (f *fakeRegistration) Register(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerN++
	if f.registerErr != nil {
		return f.registerErr
	}
	f.published = true
	return nil
}

func (f *fakeRegistration) Deregister(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deregisterN++
	if f.deregErr != nil {
		return f.deregErr
	}
	f.published = false
	return nil
}

func (f *fakeRegistration) Published() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.published
}

func (f *fakeRegistration) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeN++
}

// loseLease 模拟 keepalive 断开：key 随租约消失，但 manager 不知情。
func (f *fakeRegistration) loseLease() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = false
}

func (f *fakeRegistration) counts() (register, deregister, close int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerN, f.deregisterN, f.closeN
}

// TestRegistrationManager_RegistersOnFirstReady 验证初始状态不注册，
// 第一次就绪探测才写入服务发现。
func TestRegistrationManager_RegistersOnFirstReady(t *testing.T) {
	fake := &fakeRegistration{}
	m := newRegistrationManager(fake, 2)

	if register, _, _ := fake.counts(); register != 0 {
		t.Fatalf("构造后不应立即注册，实际注册 %d 次", register)
	}

	m.Observe(context.Background(), true)
	if register, _, _ := fake.counts(); register != 1 {
		t.Fatalf("首次就绪应注册一次，实际注册 %d 次", register)
	}

	m.Observe(context.Background(), true)
	if register, _, _ := fake.counts(); register != 1 {
		t.Fatalf("重复就绪不应重复注册，实际注册 %d 次", register)
	}
}

// TestRegistrationManager_DebouncesFailure 验证单次失败不摘除，
// 连续失败达到阈值才摘除。否则一次依赖抖动会让所有副本同时下线。
func TestRegistrationManager_DebouncesFailure(t *testing.T) {
	fake := &fakeRegistration{}
	m := newRegistrationManager(fake, 2)
	m.Observe(context.Background(), true)

	m.Observe(context.Background(), false)
	if _, deregister, _ := fake.counts(); deregister != 0 {
		t.Fatalf("单次失败不应摘除，实际摘除 %d 次", deregister)
	}

	m.Observe(context.Background(), false)
	if _, deregister, _ := fake.counts(); deregister != 1 {
		t.Fatalf("连续失败达到阈值应摘除一次，实际摘除 %d 次", deregister)
	}

	m.Observe(context.Background(), false)
	if _, deregister, _ := fake.counts(); deregister != 1 {
		t.Fatalf("已摘除后继续失败不应重复摘除，实际摘除 %d 次", deregister)
	}
}

// TestRegistrationManager_ReRegistersImmediately 验证恢复不设阈值：
// 一次 ready 就重新注册，缩短摘流量后的恢复时间。
func TestRegistrationManager_ReRegistersImmediately(t *testing.T) {
	fake := &fakeRegistration{}
	m := newRegistrationManager(fake, 2)
	m.Observe(context.Background(), true)
	m.Observe(context.Background(), false)
	m.Observe(context.Background(), false)

	m.Observe(context.Background(), true)
	register, _, _ := fake.counts()
	if register != 2 {
		t.Fatalf("恢复后应立即重新注册，实际注册 %d 次", register)
	}

	m.Observe(context.Background(), true)
	register, _, _ = fake.counts()
	if register != 2 {
		t.Fatalf("恢复后重复 ready 不应重复注册，实际注册 %d 次", register)
	}
}

// TestRegistrationManager_RetriesDeregister 验证摘除失败后，
// 后续未就绪探测会继续重试，而不是把本地状态当成已摘除就不再处理。
func TestRegistrationManager_RetriesDeregister(t *testing.T) {
	fake := &fakeRegistration{deregErr: errors.New("etcd unavailable")}
	m := newRegistrationManager(fake, 1)
	m.Observe(context.Background(), true)
	m.Observe(context.Background(), false)
	m.Observe(context.Background(), false)

	if _, deregister, _ := fake.counts(); deregister != 2 {
		t.Fatalf("摘除失败后应重试，实际调用 %d 次", deregister)
	}

	fake.mu.Lock()
	fake.deregErr = nil
	fake.mu.Unlock()
	m.Observe(context.Background(), false)
	if _, deregister, _ := fake.counts(); deregister != 3 {
		t.Fatalf("恢复 etcd 后应继续重试，实际调用 %d 次", deregister)
	}
}

// TestRegistrationManager_RegisterFailureRetries 验证注册失败不会被记为已注册，
// 下一次 ready 探测会重试。
func TestRegistrationManager_RegisterFailureRetries(t *testing.T) {
	fake := &fakeRegistration{registerErr: errors.New("etcd unavailable")}
	m := newRegistrationManager(fake, 2)
	m.Observe(context.Background(), true)

	fake.mu.Lock()
	fake.registerErr = nil
	fake.mu.Unlock()
	m.Observe(context.Background(), true)
	if register, _, _ := fake.counts(); register != 2 {
		t.Fatalf("注册失败后应在下次 ready 重试，实际注册 %d 次", register)
	}
}

// TestRegistrationManager_ReRegistersAfterLeaseLoss 验证 lease 失效后，
// 即使 manager 仍认为已注册，也会因为 Published()==false 而重新注册。
func TestRegistrationManager_ReRegistersAfterLeaseLoss(t *testing.T) {
	fake := &fakeRegistration{}
	m := newRegistrationManager(fake, 2)
	m.Observe(context.Background(), true)

	fake.loseLease()
	m.Observe(context.Background(), true)

	if register, _, _ := fake.counts(); register != 2 {
		t.Fatalf("lease 失效后应重新注册，实际注册 %d 次", register)
	}
	if !fake.Published() {
		t.Fatal("重新注册后 key 应重新发布")
	}
}

// TestRegistrationManager_CloseIsIdempotent 验证多条停机路径重复调用也安全。
func TestRegistrationManager_CloseIsIdempotent(t *testing.T) {
	fake := &fakeRegistration{}
	m := newRegistrationManager(fake, 2)
	m.Close()
	m.Close()

	if _, _, closeN := fake.counts(); closeN != 1 {
		t.Fatalf("Close 应幂等，实际调用 %d 次", closeN)
	}

	// 关闭后不应再注册，避免停机过程中 watcher 又把实例写回去。
	m.Observe(context.Background(), true)
	if register, _, _ := fake.counts(); register != 0 {
		t.Fatalf("关闭后不应注册，实际注册 %d 次", register)
	}
}

// TestServiceAddressPrecedence 验证注册地址优先级：
// 显式 Host > SERVICE_HOST > HOSTNAME > 127.0.0.1。
func TestServiceAddressPrecedence(t *testing.T) {
	t.Setenv("SERVICE_HOST", "service-host")
	t.Setenv("HOSTNAME", "pod-name")

	cases := []struct {
		name string
		host string
		want string
	}{
		{name: "显式 Host 优先", host: "explicit-host", want: "explicit-host:9000"},
		{name: "SERVICE_HOST 次之", host: "", want: "service-host:9000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serviceAddress(ServerConfig{ServiceName: "logos.test", Host: tc.host, Port: 9000})
			if got != tc.want {
				t.Fatalf("serviceAddress() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Setenv("SERVICE_HOST", "")
	got := serviceAddress(ServerConfig{ServiceName: "logos.test", Port: 9000})
	if got != "pod-name:9000" {
		t.Fatalf("HOSTNAME 回落 = %q, want pod-name:9000", got)
	}

	t.Setenv("HOSTNAME", "")
	got = serviceAddress(ServerConfig{ServiceName: "logos.test", Port: 9000})
	if got != "127.0.0.1:9000" {
		t.Fatalf("最终回落 = %q, want 127.0.0.1:9000", got)
	}
}

// TestKeepRegistered_RepairsLostRegistration 验证无依赖探针的服务
// 在 lease 失效后能靠校正循环重新注册，而不是一直留在服务发现之外。
func TestKeepRegistered_RepairsLostRegistration(t *testing.T) {
	fake := &fakeRegistration{}
	m := newRegistrationManager(fake, 2)
	m.Observe(context.Background(), true)
	fake.loseLease()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		keepRegistered(ctx, ServerConfig{ServiceName: "logos.test", ReadyInterval: 20 * time.Millisecond}, m)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fake.Published() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if !fake.Published() {
		t.Fatal("校正循环未把实例重新注册回服务发现")
	}
}
