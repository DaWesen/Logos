package middleware

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"Logos/pkg/client"

	"github.com/gin-gonic/gin"
)

// 路由路径到服务名的映射
var routeServiceMap = map[string]string{
	"/api/v1/user/":       "logos.user",
	"/api/v1/knowledge/":  "logos.knowledge",
	"/api/v1/search/":     "logos.search",
	"/api/v1/vector/":     "logos.vector",
	"/api/v1/question/":   "logos.question",
	"/api/v1/recommend/":  "logos.recommend",
	"/api/v1/extraction/": "logos.extraction",
	"/api/v1/collection/": "logos.collection",
	"/api/v1/message/":    "logos.message",
	"/api/v1/monitoring/": "logos.monitoring",
	"/api/v1/bot/":        "logos.bot",
	"/api/v1/billing/":    "logos.billing",
	"/api/v1/im/":         "logos.im",
	"/api/v1/chat/":       "logos.chat",
	"/api/v1/contact/":    "logos.contact",
	"/api/v1/summary/":    "logos.summary",
	"/api/v1/mcp/":        "logos.mcp",
	"/api/v1/moderation/": "logos.moderation",
}

func routeToService(path string) string {
	for prefix, svc := range routeServiceMap {
		if strings.HasPrefix(path, prefix) {
			return svc
		}
	}
	return ""
}

type metricsReporter struct {
	client    *client.MonitoringClient
	mu        sync.Mutex
	requests  map[string]*requestStats
	interval  time.Duration
	stopCh    chan struct{}
}

type requestStats struct {
	count     int64
	errors    int64
	latencyMs int64
	service   string // 关联的下游服务名
}

func newMetricsReporter(monitoringClient *client.MonitoringClient, interval time.Duration) *metricsReporter {
	return &metricsReporter{
		client:   monitoringClient,
		requests: make(map[string]*requestStats),
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

func (r *metricsReporter) record(path string, status int, latencyMs int64) {
	r.mu.Lock()
	stats, ok := r.requests[path]
	if !ok {
		stats = &requestStats{
			service: routeToService(path),
		}
		r.requests[path] = stats
	}
	r.mu.Unlock()

	atomic.AddInt64(&stats.count, 1)
	atomic.AddInt64(&stats.latencyMs, latencyMs)
	if status >= 400 {
		atomic.AddInt64(&stats.errors, 1)
	}
}

func (r *metricsReporter) start() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.flush()
		}
	}
}

func (r *metricsReporter) stop() {
	close(r.stopCh)
}

func (r *metricsReporter) flush() {
	r.mu.Lock()
	snapshot := r.requests
	r.requests = make(map[string]*requestStats)
	r.mu.Unlock()

	if len(snapshot) == 0 || r.client == nil {
		return
	}

	var inputs []client.MetricInput

	// 按 service 聚合
	type svcAgg struct {
		count       int64
		errors      int64
		totalLatMs  int64
	}
	aggByService := map[string]*svcAgg{}

	for path, stats := range snapshot {
		count := atomic.LoadInt64(&stats.count)
		errors := atomic.LoadInt64(&stats.errors)
		totalLatency := atomic.LoadInt64(&stats.latencyMs)

		if count == 0 {
			continue
		}

		// gateway 自身指标（按路径）
		inputs = append(inputs, client.MetricInput{
			ServiceName: "logos.gateway",
			Type:        5,
			Value:       float64(count),
			Unit:        "req",
			Tags:        map[string]string{"path": path, "period": r.interval.String()},
		})

		if errors > 0 {
			inputs = append(inputs, client.MetricInput{
				ServiceName: "logos.gateway",
				Type:        6,
				Value:       float64(errors) / float64(count) * 100,
				Unit:        "%",
				Tags:        map[string]string{"path": path},
			})
		}

		inputs = append(inputs, client.MetricInput{
			ServiceName: "logos.gateway",
			Type:        7,
			Value:       float64(totalLatency) / float64(count),
			Unit:        "ms",
			Tags:        map[string]string{"path": path},
		})

		inputs = append(inputs, client.MetricInput{
			ServiceName: "logos.gateway",
			Type:        8,
			Value:       float64(count) / r.interval.Seconds(),
			Unit:        "req/s",
			Tags:        map[string]string{"path": path},
		})

		// 下游服务指标（按服务聚合）
		if stats.service != "" {
			agg, ok := aggByService[stats.service]
			if !ok {
				agg = &svcAgg{}
				aggByService[stats.service] = agg
			}
			agg.count += count
			agg.errors += errors
			agg.totalLatMs += totalLatency
		}
	}

	// 上报下游服务的聚合指标
	for svc, agg := range aggByService {
		if agg.count == 0 {
			continue
		}
		inputs = append(inputs, client.MetricInput{
			ServiceName: svc,
			Type:        5,
			Value:       float64(agg.count),
			Unit:        "req",
			Tags:        map[string]string{"period": r.interval.String()},
		})

		if agg.errors > 0 {
			inputs = append(inputs, client.MetricInput{
				ServiceName: svc,
				Type:        6,
				Value:       float64(agg.errors) / float64(agg.count) * 100,
				Unit:        "%",
			})
		}

		inputs = append(inputs, client.MetricInput{
			ServiceName: svc,
			Type:        7,
			Value:       float64(agg.totalLatMs) / float64(agg.count),
			Unit:        "ms",
		})

		inputs = append(inputs, client.MetricInput{
			ServiceName: svc,
			Type:        8,
			Value:       float64(agg.count) / r.interval.Seconds(),
			Unit:        "req/s",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.client.BatchRecordMetrics(ctx, inputs); err != nil {
		log.Printf("[MetricsReporter] Failed to flush metrics: %v", err)
	}
}

var globalReporter *metricsReporter

func InitMetricsReporter(monitoringClient *client.MonitoringClient) {
	if globalReporter != nil {
		return
	}
	globalReporter = newMetricsReporter(monitoringClient, 30*time.Second)
	go globalReporter.start()
	log.Println("[MetricsReporter] Started, interval: 30s")
}

func StopMetricsReporter() {
	if globalReporter != nil {
		globalReporter.stop()
	}
}

func MetricsReporterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		if path == "" || path == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		latencyMs := time.Since(start).Milliseconds()

		if globalReporter != nil {
			globalReporter.record(path, c.Writer.Status(), latencyMs)
		}
	}
}
