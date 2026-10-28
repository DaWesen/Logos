package service

import (
	"context"
	"sync"
	"time"

	"Logos/internal/service/platform/billing/dao"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	defaultPaymentOrderReapInterval = time.Minute
	defaultPaymentOrderReapBatch    = 200
)

// PaymentOrderReaper 周期性回收已过期仍为 pending 的支付单。
//
// 按需回收（查询订单、渠道回调时 expireIfNeeded）只能覆盖"有人访问"的订单，
// 用户下单后直接关掉页面的 pending 单永远不会被访问，会一直残留：
// 订单状态统计失真、表持续膨胀、对账时无法区分"已过期"与"还在等支付"。
// 这里用有界批量更新兜底，与按需回收形成分层：单条精确回收 + 批量兜底。
type PaymentOrderReaper struct {
	repo     dao.BillingRepository
	interval time.Duration
	batch    int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type PaymentOrderReaperOption func(*PaymentOrderReaper)

// WithReapInterval 设置回收周期。
func WithReapInterval(d time.Duration) PaymentOrderReaperOption {
	return func(r *PaymentOrderReaper) {
		if d > 0 {
			r.interval = d
		}
	}
}

// WithReapBatch 设置单批回收上限，避免一次更新大量数据。
func WithReapBatch(n int) PaymentOrderReaperOption {
	return func(r *PaymentOrderReaper) {
		if n > 0 {
			r.batch = n
		}
	}
}

func NewPaymentOrderReaper(repo dao.BillingRepository, opts ...PaymentOrderReaperOption) *PaymentOrderReaper {
	r := &PaymentOrderReaper{
		repo:     repo,
		interval: defaultPaymentOrderReapInterval,
		batch:    defaultPaymentOrderReapBatch,
	}
	for _, opt := range opts {
		opt(r)
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	return r
}

// Start 启动后台回收循环（非阻塞）：先立即执行一次，随后按周期触发。
func (r *PaymentOrderReaper) Start() {
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

// Stop 停止回收循环并等待当前批次执行完毕，保证优雅退出时不中断正在进行的更新。
func (r *PaymentOrderReaper) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *PaymentOrderReaper) reapOnce(ctx context.Context) {
	reaped, err := r.repo.ExpirePendingOrders(ctx, time.Now(), r.batch)
	if err != nil {
		// 回收失败不影响支付主链路，下一周期重试即可
		logger.Warn("回收过期支付单失败", logger.ErrorField(err))
		return
	}
	if reaped == 0 {
		return
	}

	obs.RecordPaymentOrderReaped(ctx, reaped)
	logger.Info("回收过期支付单", logger.Int64Field("count", reaped))
}
