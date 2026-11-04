package service

import (
	"context"
	"sync"
	"time"

	"Logos/internal/service/ai/process/dao"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	defaultDocumentReapInterval = 5 * time.Minute
	defaultDocumentReapBatch    = 200
	defaultDocumentStaleAfter   = 30 * time.Minute
)

const staleDocumentReason = "处理超时：进程重启或任务中断，请重新上传"

// StaleDocumentReaper 周期性回收长时间停留在 Pending/Processing 的文档。
//
// 文档处理是进程内异步任务：进程重启、OOM、节点漂移都会让「文档已建、任务没跑完」
// 永久卡住，用户看到一直「处理中」，后台也没有任何自愈动作。
// 这里用有界批量更新兜底：只处理 updated_at 超过阈值的记录，
// 阈值必须明显大于单篇文档的最长处理时间（解析 + 3 分钟向量化），避免误杀在途任务。
type StaleDocumentReaper struct {
	repo       dao.ProcessRepository
	interval   time.Duration
	batch      int
	staleAfter time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type StaleDocumentReaperOption func(*StaleDocumentReaper)

func WithDocumentReapInterval(d time.Duration) StaleDocumentReaperOption {
	return func(r *StaleDocumentReaper) {
		if d > 0 {
			r.interval = d
		}
	}
}

func WithDocumentReapBatch(n int) StaleDocumentReaperOption {
	return func(r *StaleDocumentReaper) {
		if n > 0 {
			r.batch = n
		}
	}
}

func WithDocumentStaleAfter(d time.Duration) StaleDocumentReaperOption {
	return func(r *StaleDocumentReaper) {
		if d > 0 {
			r.staleAfter = d
		}
	}
}

func NewStaleDocumentReaper(repo dao.ProcessRepository, opts ...StaleDocumentReaperOption) *StaleDocumentReaper {
	r := &StaleDocumentReaper{
		repo:       repo,
		interval:   defaultDocumentReapInterval,
		batch:      defaultDocumentReapBatch,
		staleAfter: defaultDocumentStaleAfter,
	}
	for _, opt := range opts {
		opt(r)
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	return r
}

// Start 非阻塞启动：先立即回收一次（覆盖上一次进程留下的孤儿），随后按周期执行。
func (r *StaleDocumentReaper) Start() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		r.reapOnce(r.ctx)

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				r.reapOnce(r.ctx)
			}
		}
	}()
}

// Stop 等待当前批次结束再退出，避免优雅停机时留下半截更新。
func (r *StaleDocumentReaper) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *StaleDocumentReaper) reapOnce(ctx context.Context) {
	cutoff := time.Now().Add(-r.staleAfter)
	count, err := r.repo.FailStaleDocuments(ctx, cutoff, r.batch, staleDocumentReason)
	if err != nil {
		obs.RecordDocumentReapFailure(ctx)
		logger.Warn("回收孤儿文档失败", logger.ErrorField(err))
		return
	}
	if count == 0 {
		return
	}
	obs.RecordDocumentReaped(ctx, count)
	logger.Info("回收孤儿文档",
		logger.Int64Field("count", count),
		logger.StringField("stale_after", r.staleAfter.String()))
}
