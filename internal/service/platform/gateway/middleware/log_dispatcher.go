package middleware

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	pb "Logos/proto_gen/monitoring"

	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	defaultLogQueueCapacity = 4096
	defaultLogWorkers       = 4
	logReportTimeout        = 3 * time.Second
)

// logRecord 是一条待异步上报的访问日志。
// 字段在入队时就固定下来，避免 worker 读取已被 gin 回收的 *gin.Context。
type logRecord struct {
	serviceName string
	level       string
	message     string
	fields      map[string]string
}

// logSink 只依赖 RecordLog 一个方法，测试可以用很小的 fake 替代整个 gRPC 客户端。
type logSink interface {
	RecordLog(ctx context.Context, req *pb.RecordLogReq) error
}

type grpcLogSink struct {
	client pb.MonitoringServiceClient
}

func (s grpcLogSink) RecordLog(ctx context.Context, req *pb.RecordLogReq) error {
	_, err := s.client.RecordLog(ctx, req)
	return err
}

// logDispatcher 用固定 worker 消费有界队列。
//
// 为什么不是「每个请求 go func()」：请求量由外部决定，监控服务一慢，
// 每个请求都留下一个阻塞在 RPC 上的 goroutine，内存和 goroutine 数会随流量线性上涨，
// 最终把网关自己拖垮。有界队列把「日志上报」变成可丢弃的旁路：
// 队列满时丢日志并计数，主链路永远不被观测系统反向压垮。
type logDispatcher struct {
	queue     chan logRecord
	sink      logSink
	workers   int
	stopCh    chan struct{}
	abortCh   chan struct{}
	closeOnce sync.Once
	abortOnce sync.Once
	stopped   atomic.Bool
	lastWarn  atomic.Int64
	wg        sync.WaitGroup
}

func newLogDispatcher(sink logSink, capacity, workers int) *logDispatcher {
	if capacity <= 0 {
		capacity = defaultLogQueueCapacity
	}
	if workers <= 0 {
		workers = defaultLogWorkers
	}
	return &logDispatcher{
		queue:   make(chan logRecord, capacity),
		sink:    sink,
		workers: workers,
		stopCh:  make(chan struct{}),
		abortCh: make(chan struct{}),
	}
}

func (d *logDispatcher) start() {
	d.wg.Add(d.workers)
	for i := 0; i < d.workers; i++ {
		go d.worker()
	}
}

func (d *logDispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case rec := <-d.queue:
			d.report(rec)
		case <-d.abortCh:
			return
		case <-d.stopCh:
			// 停机分两段：先排空队列里已经接收的日志；
			// 超过停机预算由 stop() 关闭 abortCh，worker 立即退出，剩余日志丢弃。
			d.drain()
			return
		}
	}
}

func (d *logDispatcher) drain() {
	for {
		select {
		case rec := <-d.queue:
			d.report(rec)
		case <-d.abortCh:
			return
		default:
			// 队列已空且 stopped=true 之后不会再有新日志入队。
			return
		}
	}
}

func (d *logDispatcher) report(rec logRecord) {
	if d.sink == nil {
		return
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), logReportTimeout)
	defer cancel()

	err := d.sink.RecordLog(ctx, &pb.RecordLogReq{
		ServiceName: rec.serviceName,
		Level:       rec.level,
		Message:     rec.message,
		Fields:      rec.fields,
	})
	duration := time.Since(start).Seconds()
	if err != nil {
		obs.RecordGatewayLogReport(context.Background(), "error", duration)
		d.warnThrottled(err)
		return
	}
	obs.RecordGatewayLogReport(context.Background(), "ok", duration)
}

// warnThrottled 限制失败日志的频率。
//
// 监控服务整体不可用时，每条日志都会失败；如果每条都打 Warn，
// 日志系统会先于监控服务被打爆，真正的故障现场反而被刷掉。
// 这里按 10s 一条采样，失败总量仍由 logos.gateway.log_report.count{result=error} 计数。
func (d *logDispatcher) warnThrottled(err error) {
	const warnInterval = int64(10 * time.Second)
	now := time.Now().UnixNano()
	last := d.lastWarn.Load()
	if now-last < warnInterval || !d.lastWarn.CompareAndSwap(last, now) {
		return
	}
	logger.Warn("访问日志上报失败（同类失败 10s 内只打印一次）", logger.ErrorField(err))
}

// enqueue 是非阻塞的：队列满时立即丢弃并打点，绝不让业务请求等待日志上报。
func (d *logDispatcher) enqueue(rec logRecord) bool {
	if d == nil || d.stopped.Load() {
		return false
	}
	select {
	case d.queue <- rec:
		obs.RecordGatewayLogReport(context.Background(), "enqueued", 0)
		return true
	default:
		obs.RecordGatewayLogReport(context.Background(), "dropped", 0)
		return false
	}
}

// stop 先让 worker 排空已入队的日志，最多等 timeout；超时强制退出，剩余日志丢弃。
func (d *logDispatcher) stop(timeout time.Duration) {
	if d == nil {
		return
	}
	d.closeOnce.Do(func() {
		d.stopped.Store(true)
		close(d.stopCh)
	})

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		d.abortOnce.Do(func() { close(d.abortCh) })
		logger.Warn("日志上报 worker 未在超时内退出")
	}
}

var (
	globalLogDispatcher   *logDispatcher
	globalLogDispatcherMu sync.RWMutex
)

// InitLogDispatcher 由网关启动时调用；重复调用只保留第一个实例。
func InitLogDispatcher(client pb.MonitoringServiceClient) {
	if client == nil {
		return
	}
	globalLogDispatcherMu.Lock()
	defer globalLogDispatcherMu.Unlock()
	if globalLogDispatcher != nil {
		return
	}
	globalLogDispatcher = newLogDispatcher(grpcLogSink{client: client}, defaultLogQueueCapacity, defaultLogWorkers)
	globalLogDispatcher.start()
}

// StopLogDispatcher 在网关优雅停机时调用，给队列中的日志一个有限的收尾窗口。
func StopLogDispatcher() {
	globalLogDispatcherMu.Lock()
	d := globalLogDispatcher
	globalLogDispatcher = nil
	globalLogDispatcherMu.Unlock()
	if d != nil {
		d.stop(5 * time.Second)
	}
}

func getLogDispatcher() *logDispatcher {
	globalLogDispatcherMu.RLock()
	defer globalLogDispatcherMu.RUnlock()
	return globalLogDispatcher
}
