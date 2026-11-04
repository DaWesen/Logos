package middleware

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	pb "Logos/proto_gen/monitoring"
)

type fakeLogSink struct {
	started chan struct{}
	release chan struct{}
	block   atomic.Bool
	count   atomic.Int64
	err     error
}

func newFakeLogSink() *fakeLogSink {
	return &fakeLogSink{
		started: make(chan struct{}, 64),
		release: make(chan struct{}),
	}
}

func (s *fakeLogSink) RecordLog(ctx context.Context, req *pb.RecordLogReq) error {
	s.count.Add(1)
	if s.block.Load() {
		select {
		case s.started <- struct{}{}:
		default:
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}

// TestLogDispatcher_DropsWhenFull 队列满时必须立即丢弃，不能阻塞请求。
func TestLogDispatcher_DropsWhenFull(t *testing.T) {
	sink := newFakeLogSink()
	sink.block.Store(true)
	d := newLogDispatcher(sink, 2, 1)
	d.start()
	defer d.stop(time.Second)
	defer close(sink.release)

	if !d.enqueue(logRecord{serviceName: "logos.gateway"}) {
		t.Fatal("队列有空位时应入队成功")
	}
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("worker 未开始消费")
	}

	// worker 被阻塞，队列容量 2，再入队两条刚好填满，第三条必须丢弃。
	if !d.enqueue(logRecord{serviceName: "logos.gateway"}) {
		t.Fatal("第 2 条应入队")
	}
	if !d.enqueue(logRecord{serviceName: "logos.gateway"}) {
		t.Fatal("第 3 条应入队")
	}
	if d.enqueue(logRecord{serviceName: "logos.gateway"}) {
		t.Fatal("队列满时应丢弃而不是阻塞")
	}
}

// TestLogDispatcher_ProcessesAndStops worker 正常消费，stop 后不再接收。
func TestLogDispatcher_ProcessesAndStops(t *testing.T) {
	sink := newFakeLogSink()
	d := newLogDispatcher(sink, 8, 2)
	d.start()

	const total = 8
	for i := 0; i < total; i++ {
		if !d.enqueue(logRecord{serviceName: "logos.gateway"}) {
			t.Fatalf("第 %d 条入队失败", i)
		}
	}

	deadline := time.Now().Add(time.Second)
	for sink.count.Load() < total && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sink.count.Load(); got != total {
		t.Fatalf("处理条数 = %d，期望 %d", got, total)
	}

	d.stop(time.Second)
	if d.enqueue(logRecord{serviceName: "logos.gateway"}) {
		t.Fatal("stop 后不应再接收日志")
	}
}

// TestLogDispatcher_ReportErrorIsCounted 上报失败只记录，不影响 worker 继续消费。
func TestLogDispatcher_ReportErrorIsCounted(t *testing.T) {
	sink := newFakeLogSink()
	sink.err = errors.New("monitoring unavailable")
	d := newLogDispatcher(sink, 4, 1)
	d.start()

	for i := 0; i < 3; i++ {
		if !d.enqueue(logRecord{serviceName: "logos.gateway"}) {
			t.Fatalf("第 %d 条入队失败", i)
		}
	}

	deadline := time.Now().Add(time.Second)
	for sink.count.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sink.count.Load(); got != 3 {
		t.Fatalf("上报失败后 worker 应继续消费，实际处理 %d 条", got)
	}
	d.stop(time.Second)
}

// TestLogDispatcher_NilAndZeroCapacity 空 dispatcher 与非法容量都要安全降级。
func TestLogDispatcher_NilAndZeroCapacity(t *testing.T) {
	var d *logDispatcher
	if d.enqueue(logRecord{}) {
		t.Fatal("nil dispatcher 不应入队")
	}

	zero := newLogDispatcher(newFakeLogSink(), 0, 0)
	if cap(zero.queue) != defaultLogQueueCapacity {
		t.Fatalf("容量 = %d，期望默认值 %d", cap(zero.queue), defaultLogQueueCapacity)
	}
	if zero.workers != defaultLogWorkers {
		t.Fatalf("worker 数 = %d，期望默认值 %d", zero.workers, defaultLogWorkers)
	}
}

// TestLogDispatcher_StopDrainsQueue 停机应先排空已入队的日志，
// 而不是一收到停止信号就退出，否则「有限收尾窗口」只是注释里的承诺。
func TestLogDispatcher_StopDrainsQueue(t *testing.T) {
	sink := newFakeLogSink()
	d := newLogDispatcher(sink, 16, 2)
	d.start()

	const total = 10
	for i := 0; i < total; i++ {
		if !d.enqueue(logRecord{serviceName: "logos.gateway"}) {
			t.Fatalf("第 %d 条入队失败", i)
		}
	}

	d.stop(2 * time.Second)
	if got := sink.count.Load(); got != total {
		t.Fatalf("停机后应排空 %d 条，实际 %d 条", total, got)
	}
}
