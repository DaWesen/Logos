package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"Logos/internal/platform/monitoring/model"
	"Logos/pkg/logger"

	"go.etcd.io/etcd/client/v3"
	"gorm.io/gorm"
)

// Noah 微服务列表（已知的服务名）
var noahServices = []string{
	"logos.user",
	"logos.knowledge",
	"logos.question",
	"logos.recommend",
	"logos.collection",
	"logos.vector",
	"logos.search",
	"logos.extraction",
	"logos.message",
	"logos.monitoring",
}

type ServiceCollector struct {
	db       *gorm.DB
	etcdCli  *clientv3.Client
	interval time.Duration
}

func NewServiceCollector(db *gorm.DB, etcdEndpoints []string, interval time.Duration) (*ServiceCollector, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   etcdEndpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &ServiceCollector{
		db:       db,
		etcdCli:  cli,
		interval: interval,
	}, nil
}

func (c *ServiceCollector) Start(ctx context.Context) {
	log.Println("[Collector] 服务发现采集器启动，间隔:", c.interval)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// 首次延迟10秒执行，等待 Kitex 服务注册完成
	select {
	case <-ctx.Done():
		log.Println("[Collector] 服务发现采集器停止")
		return
	case <-time.After(10 * time.Second):
		c.collectServices(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[Collector] 服务发现采集器停止")
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
	discovered := make(map[string]bool)

	for _, svcName := range noahServices {
		// 通过 etcd 查询该服务是否有注册的实例
		// gRPC service key format: /grpc/<service_name>/<host:port>
		prefix := fmt.Sprintf("/grpc/%s/", svcName)
		resp, err := c.etcdCli.Get(ctx, prefix, clientv3.WithPrefix())
		if err != nil {
			logger.Error("etcd 查询服务失败",
				logger.StringField("service", svcName),
				logger.ErrorField(err))
			// etcd 查询失败，可能是连接问题，不更新状态
			continue
		}

		instanceCount := len(resp.Kvs)
		status := "UP"
		var errMsg *string

		if instanceCount == 0 {
			status = "DOWN"
			msg := "未在 etcd 中发现服务实例"
			errMsg = &msg
		}

		discovered[svcName] = true

		metadata := map[string]string{
			"instances": fmt.Sprintf("%d", instanceCount),
		}
		metadataJSON, _ := json.Marshal(metadata)

		// 先查找，不存在则创建，再更新
		var ss model.ServiceStatus
		result := c.db.WithContext(ctx).Where("service_name = ?", svcName).Attrs(model.ServiceStatus{
			ServiceName: svcName,
		}).FirstOrCreate(&ss)
		if result.Error != nil {
			logger.Error("查找/创建服务状态失败",
				logger.StringField("service", svcName),
				logger.ErrorField(result.Error))
			continue
		}
		if err := c.db.WithContext(ctx).Model(&ss).Updates(map[string]any{
			"status":          status,
			"last_check_time": time.Now().UnixMilli(),
			"error_message":   errMsg,
			"metadata":        string(metadataJSON),
		}).Error; err != nil {
			logger.Error("更新服务状态失败",
				logger.StringField("service", svcName),
				logger.ErrorField(err))
		}
	}

	logger.Info("服务状态采集完成",
		logger.IntField("service_count", len(discovered)))
}
