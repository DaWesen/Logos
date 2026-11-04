package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"Logos/internal/service/platform/monitoring/model"
	"Logos/pkg/logger"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Noa service list to monitor
var noahServices = []string{
	"logos.gateway",
	"logos.user",
	"logos.billing",
	"logos.monitoring",
	"logos.knowledge",
	"logos.question",
	"logos.recommend",
	"logos.collection",
	"logos.vector",
	"logos.search",
	"logos.extraction",
	"logos.summary",
	"logos.moderation",
	"logos.bot",
	"logos.mcp",
	"logos.message",
	"logos.chat",
	"logos.contact",
	"logos.im",
}

type ServiceCollector struct {
	db           *gorm.DB
	etcdCli      *clientv3.Client
	interval     time.Duration
	queryTimeout time.Duration
}

// etcdQueryTimeout 单次采集的 etcd 查询预算。
// 采集器跑在独立 goroutine 里，但 etcd 不可达时 client 调用可能长时间阻塞；
// 没有超时会让整个采集循环停摆，状态表从此不再更新，故障反而被掩盖成「最后一次正常」。
const etcdQueryTimeout = 10 * time.Second

// registrationPrefix 是所有 gRPC 服务注册 key 的公共前缀。
// 一次 WithPrefix 查询即可拿到全部实例，不必为每个服务单独往返一次。
const registrationPrefix = "/grpc/"

func NewServiceCollector(db *gorm.DB, etcdEndpoints []string, interval time.Duration) (*ServiceCollector, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   etcdEndpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &ServiceCollector{
		db:           db,
		etcdCli:      cli,
		interval:     interval,
		queryTimeout: etcdQueryTimeout,
	}, nil
}

func (c *ServiceCollector) Start(ctx context.Context) {
	log.Println("[Collector] Starting service status collector with interval:", c.interval)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// First execution after 10s, wait for services to register
	select {
	case <-ctx.Done():
		log.Println("[Collector] Service status collector stopped")
		return
	case <-time.After(10 * time.Second):
		c.collectServices(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[Collector] Service status collector stopped")
			return
		case <-ticker.C:
			c.collectServices(ctx)
		}
	}
}

func (c *ServiceCollector) Close() {
	if c.etcdCli != nil {
		c.etcdCli.Close()
	}
}

func (c *ServiceCollector) collectServices(ctx context.Context) {
	queryCtx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	resp, err := c.etcdCli.Get(queryCtx, registrationPrefix, clientv3.WithPrefix())
	if err != nil {
		// 关键区别：查不到 ≠ 服务下线。etcd 本身故障时若把所有服务标成 DOWN，
		// 会把「监控系统看不到」误报成「业务全挂」，掩盖真正的故障范围。
		// 保留上一次状态，只更新检查时间与错误信息，让界面显示 DEGRADED。
		logger.Error("Failed to query etcd registrations, keeping previous statuses",
			logger.ErrorField(err))
		c.markDegraded(ctx, err)
		return
	}

	counts := instanceCounts(resp.Kvs)
	now := time.Now().UnixMilli()
	rows := make([]*model.ServiceStatus, 0, len(noahServices))
	for _, svcName := range noahServices {
		instanceCount := counts[svcName]
		status := "UP"
		var errMsg *string
		if instanceCount == 0 {
			status = "DOWN"
			msg := "No service instances found in etcd"
			errMsg = &msg
		}
		metadataJSON, _ := json.Marshal(map[string]string{
			"instances": fmt.Sprintf("%d", instanceCount),
		})
		rows = append(rows, &model.ServiceStatus{
			ServiceName:   svcName,
			Status:        status,
			LastCheckTime: now,
			ErrorMessage:  errMsg,
			Metadata:      string(metadataJSON),
		})
	}

	if err := c.upsertStatuses(ctx, rows); err != nil {
		logger.Error("Failed to persist service statuses", logger.ErrorField(err))
		return
	}
	logger.Info("Service status collection complete",
		logger.IntField("service_count", len(rows)))
}

// instanceCounts 从一次前缀查询结果里按服务名计数。
//
// key 形如 /grpc/<service>/<host:port>，服务名本身可能包含 '/'，
// 因此不能用 SplitN 取固定下标；改为从右侧切掉 host:port，
// 剩下的部分才是完整服务名。
func instanceCounts(kvs []*mvccpb.KeyValue) map[string]int {
	counts := make(map[string]int, len(kvs))
	for _, kv := range kvs {
		key := string(kv.Key)
		if !strings.HasPrefix(key, registrationPrefix) {
			continue
		}
		rest := strings.TrimPrefix(key, registrationPrefix)
		idx := strings.LastIndex(rest, "/")
		if idx <= 0 {
			continue
		}
		counts[rest[:idx]]++
	}
	return counts
}

// upsertStatuses 用一条 INSERT ... ON CONFLICT DO UPDATE 批量写入。
// 逐条 FirstOrCreate + Updates 是两次往返，19 个服务就是 38 次；
// 采集是周期性任务，这些往返会与业务查询争抢连接池。
func (c *ServiceCollector) upsertStatuses(ctx context.Context, rows []*model.ServiceStatus) error {
	if len(rows) == 0 {
		return nil
	}
	return c.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "service_name"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"status", "last_check_time", "error_message", "metadata", "updated_at",
			}),
		}).
		Create(&rows).Error
}

// markDegraded 在 etcd 不可达时保留上一次的 UP/DOWN 判定，
// 只把状态降级为 DEGRADED 并写明原因。
//
// 不能直接标 DOWN：etcd 故障期间所有服务都会变成 DOWN，
// 运维看到的是「全站宕机」，实际可能只是监控自身失明。
func (c *ServiceCollector) markDegraded(ctx context.Context, cause error) {
	msg := fmt.Sprintf("etcd query failed, status may be stale: %v", cause)
	now := time.Now().UnixMilli()
	err := c.db.WithContext(ctx).
		Model(&model.ServiceStatus{}).
		Where("service_name IN ?", noahServices).
		Updates(map[string]any{
			"status":          "DEGRADED",
			"last_check_time": now,
			"error_message":   msg,
		}).Error
	if err != nil {
		logger.Error("Failed to mark service statuses degraded", logger.ErrorField(err))
	}
}
