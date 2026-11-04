package grpcserver

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// registration 是「可摘除、可恢复」的服务注册抽象。
//
// 不能再用一次性的 deregister 闭包：它就关掉 etcd 连接、撤销 lease，
// 依赖恢复后没有任何办法把实例重新加回去，只能重启进程。
type registration interface {
	Register(ctx context.Context) error
	Deregister(ctx context.Context) error
	// Published 表示注册 key 当前是否真的存在于 etcd。
	// 不能只信 manager 自己的布尔值：lease 保活断开时 key 会随租约消失，
	// 此时 manager 仍以为已注册，恢复后就再也不会 Put 回去。
	Published() bool
	Close()
}

// etcdRegistrar 维护一个可重复注册的 etcd key 与长租约。
//
// 设计取舍：
//   - 未就绪时只删除 key，不撤销 lease 与连接。恢复时可以立刻 Put 回去，
//     不必重新建连、重新 Grant，缩短摘流量后的恢复时间；
//   - lease 在未注册期间仍然保活。若 etcd 重启/租约过期，keepalive 会失败，
//     下次 Register 会重新 Grant，而不是拿着已失效的 lease 一直 Put 失败；
//   - 所有状态变更串行化。注册频率很低（只在就绪状态翻转时），
//     用一把锁换正确性，比无锁状态机更容易审计。
type etcdRegistrar struct {
	client      *clientv3.Client
	serviceName string
	key         string
	value       string

	mu              sync.Mutex
	leaseID         clientv3.LeaseID
	published       bool
	closed          bool
	keepAliveCancel context.CancelFunc
	keepAliveDone   chan struct{}
}

// leaseTTL 与原来的注册逻辑保持一致：60s 足够覆盖短暂网络抖动，
// 又不会让一个已死实例在服务发现里停留太久。
const leaseTTL = 60

func newEtcdRegistrar(cfg ServerConfig) (*etcdRegistrar, error) {
	dialTimeout := cfg.Etcd.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Etcd.Endpoints,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	r := &etcdRegistrar{
		client:      client,
		serviceName: cfg.ServiceName,
		value:       serviceAddress(cfg),
	}
	r.key = fmt.Sprintf("/grpc/%s/%s", r.serviceName, r.value)
	return r, nil
}

// serviceAddress 解析注册到 etcd 的地址。
//
// 顺序：显式 Host > SERVICE_HOST > HOSTNAME > 127.0.0.1。
// 容器里 HOSTNAME 通常是 Pod 名，K8s 的 headless/service DNS 不一定能解析，
// 因此生产环境应显式配置 SERVICE_HOST；本地开发才回落到回环地址。
func serviceAddress(cfg ServerConfig) string {
	hostname := cfg.Host
	if hostname == "" {
		hostname = os.Getenv("SERVICE_HOST")
	}
	if hostname == "" {
		hostname = os.Getenv("HOSTNAME")
	}
	if hostname == "" {
		hostname = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", hostname, cfg.Port)
}

// Register 把实例写回服务发现。
//
// 这里不做「已注册就跳过」的短路：上一次摘除可能因为网络失败而没删掉 key，
// 此时本地状态仍是 published，直接跳过会让实例以为已经恢复注册。
// 由 registrationManager 负责抑制重复调用，注册器本身始终执行 Put。
func (r *etcdRegistrar) Register(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return fmt.Errorf("etcd registrar: closed")
	}

	if r.leaseID == 0 {
		if err := r.grantLeaseLocked(ctx); err != nil {
			return err
		}
	}

	if _, err := r.client.Put(ctx, r.key, r.value, clientv3.WithLease(r.leaseID)); err != nil {
		// lease 可能已经过期（例如 etcd 重启后 keepalive 尚未发现）。
		// 丢弃旧 lease，重新 Grant 后再试一次；仍失败才上报。
		log.Printf("etcd register with lease %d failed, retrying with a new lease: %v", r.leaseID, err)
		r.stopKeepAliveLocked()
		r.leaseID = 0
		if grantErr := r.grantLeaseLocked(ctx); grantErr != nil {
			return grantErr
		}
		if _, retryErr := r.client.Put(ctx, r.key, r.value, clientv3.WithLease(r.leaseID)); retryErr != nil {
			return fmt.Errorf("failed to register service with etcd: %w", retryErr)
		}
	}

	r.published = true
	log.Printf("Service %s registered with etcd at %s", r.serviceName, r.value)
	return nil
}

// Deregister 只删除 key，保留 lease 与连接，便于依赖恢复后快速重新注册。
func (r *etcdRegistrar) Deregister(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed || !r.published {
		return nil
	}
	if _, err := r.client.Delete(ctx, r.key); err != nil {
		// 删除失败时 lease 过期兜底：key 最终会消失，但会慢 60s。
		// 调用方仍应把实例视为已摘除（健康状态已经 NOT_SERVING），
		// 不能因为一次删除失败就重新接流量。
		log.Printf("etcd deregister failed (will expire via lease): %v", err)
		return fmt.Errorf("failed to deregister from etcd: %w", err)
	}
	r.published = false
	log.Printf("Service %s deregistered from etcd (not ready)", r.serviceName)
	return nil
}

// Published 返回注册 key 是否应当存在于 etcd。
func (r *etcdRegistrar) Published() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.published
}

// Close 是停机路径：删除 key、撤销 lease、停保活、关连接。
func (r *etcdRegistrar) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	cancel := r.keepAliveCancel
	done := r.keepAliveDone
	leaseID := r.leaseID
	published := r.published
	r.keepAliveCancel = nil
	r.keepAliveDone = nil
	r.leaseID = 0
	r.published = false
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()
	if published {
		if _, err := r.client.Delete(ctx, r.key); err != nil {
			log.Printf("etcd deregister on shutdown failed (will expire via lease): %v", err)
		}
	}
	if leaseID != 0 {
		_, _ = r.client.Revoke(ctx, leaseID)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			log.Printf("etcd keepalive goroutine did not stop within 2s")
		}
	}
	_ = r.client.Close()
}

func (r *etcdRegistrar) grantLeaseLocked(ctx context.Context) error {
	lease, err := r.client.Grant(ctx, leaseTTL)
	if err != nil {
		return fmt.Errorf("failed to create etcd lease: %w", err)
	}
	r.leaseID = lease.ID
	r.startKeepAliveLocked()
	return nil
}

func (r *etcdRegistrar) startKeepAliveLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.keepAliveCancel = cancel
	r.keepAliveDone = done
	leaseID := r.leaseID

	go func() {
		defer close(done)
		ch, err := r.client.KeepAlive(ctx, leaseID)
		if err != nil {
			r.onKeepAliveLost(leaseID, err)
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					r.onKeepAliveLost(leaseID, fmt.Errorf("keepalive channel closed"))
					return
				}
			}
		}
	}()
}

// stopKeepAliveLocked 只取消旧 goroutine，不等待它退出。
// 调用方持有 r.mu 时等待会与 onKeepAliveLost 抢锁，形成死锁。
func (r *etcdRegistrar) stopKeepAliveLocked() {
	if r.keepAliveCancel != nil {
		r.keepAliveCancel()
	}
	r.keepAliveCancel = nil
	r.keepAliveDone = nil
}

func (r *etcdRegistrar) onKeepAliveLost(leaseID clientv3.LeaseID, cause error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.leaseID != leaseID {
		return
	}
	log.Printf("etcd keepalive lost for %s (lease %d): %v; will re-grant on next ready transition",
		r.serviceName, leaseID, cause)
	r.leaseID = 0
	// key 会随 lease 过期消失；标记为未注册，确保恢复时走 Grant + Put。
	r.published = false
}

// registrationManager 把「依赖是否就绪」翻译成「是否应该在服务发现里」。
//
// 两个状态机约束：
//   - 未就绪必须连续达到阈值才摘除。单次探测失败可能只是依赖抖一下，
//     立即摘除会让所有副本同时退出服务发现，把一次小抖动放大成整体故障；
//   - 恢复不设阈值。只要探测到 ready 就立即重新注册，缩短摘流量时间。
//
// 摘除失败时 manager 记录 pending，并在后续未就绪探测中重试 Delete：
// 即使 key 因为网络问题没删掉，也不能让本地状态回到「已注册」。
type registrationManager struct {
	mu                sync.Mutex
	reg               registration
	threshold         int
	failures          int
	registered        bool
	deregisterPending bool
	closed            bool
}

// registrationOpTimeout 给单次注册/摘除操作兜底。
// etcd 不可达时 client 的调用可能长时间阻塞；探测 goroutine 被挂住后，
// 后续所有健康判定都会停止，实例既不会恢复注册也无法正确摘除。
const registrationOpTimeout = 5 * time.Second

func newRegistrationManager(reg registration, threshold int) *registrationManager {
	if threshold <= 0 {
		threshold = 1
	}
	return &registrationManager{reg: reg, threshold: threshold}
}

// Observe 处理一次探测结果。ctx 应随进程生命周期取消，
// 这样停机时正在进行的 etcd 操作会被立刻打断，而不是拖住关闭流程。
func (m *registrationManager) Observe(ctx context.Context, ready bool) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}

	if ready {
		m.failures = 0
		// 即使 manager 认为已注册，也要确认 key 真的还在：
		// lease 失效时注册器会自行把 published 置 false。
		if m.registered && m.reg.Published() {
			return
		}
		opCtx, cancel := context.WithTimeout(ctx, registrationOpTimeout)
		err := m.reg.Register(opCtx)
		cancel()
		if err != nil {
			log.Printf("etcd re-register failed, will retry on next ready probe: %v", err)
			return
		}
		m.registered = true
		m.deregisterPending = false
		return
	}

	if m.failures < m.threshold {
		m.failures++
	}
	if m.failures < m.threshold {
		return
	}

	if m.registered {
		opCtx, cancel := context.WithTimeout(ctx, registrationOpTimeout)
		err := m.reg.Deregister(opCtx)
		cancel()
		if err != nil {
			log.Printf("etcd deregister failed, will retry while not ready: %v", err)
			m.deregisterPending = true
		} else {
			m.deregisterPending = false
		}
		// 无论 Delete 是否成功，本地都必须按「已摘除」处理：
		// 依赖没恢复前不能重新接流量。
		m.registered = false
		return
	}

	if m.deregisterPending {
		opCtx, cancel := context.WithTimeout(ctx, registrationOpTimeout)
		err := m.reg.Deregister(opCtx)
		cancel()
		if err != nil {
			log.Printf("etcd deregister retry failed: %v", err)
			return
		}
		m.deregisterPending = false
	}
}

// Close 标记状态机停止并释放注册器。幂等，可在多条停机路径上调用。
func (m *registrationManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()

	m.reg.Close()
}
