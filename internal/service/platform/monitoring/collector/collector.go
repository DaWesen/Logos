
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"Logos/internal/service/platform/monitoring/model"
	"Logos/pkg/logger"

	"go.etcd.io/etcd/client/v3"
	"gorm.io/gorm"
)

// Noa service list to monitor
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
	discovered := make(map[string]bool)

	for _, svcName := range noahServices {
		// Query etcd to see if there are registered instances
		// gRPC service key format: /grpc/<service_name>/<host:port>
		prefix := fmt.Sprintf("/grpc/%s/", svcName)
		resp, err := c.etcdCli.Get(ctx, prefix, clientv3.WithPrefix())
		if err != nil {
			logger.Error("Failed to query etcd service",
				logger.StringField("service", svcName),
				logger.ErrorField(err))
			// etcd query failed, skip this service for now
			continue
		}

		instanceCount := len(resp.Kvs)
		status := "UP"
		var errMsg *string

		if instanceCount == 0 {
			status = "DOWN"
			msg := "No service instances found in etcd"
			errMsg = &msg
		}

		discovered[svcName] = true

		metadata := map[string]string{
			"instances": fmt.Sprintf("%d", instanceCount),
		}
		metadataJSON, _ := json.Marshal(metadata)

		// First check if exists, create if not, then update
		var ss model.ServiceStatus
		result := c.db.WithContext(ctx).Where("service_name = ?", svcName).Attrs(model.ServiceStatus{
			ServiceName: svcName,
		}).FirstOrCreate(&ss)
		if result.Error != nil {
			logger.Error("Failed to find or create service status",
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
			logger.Error("Failed to update service status",
				logger.StringField("service", svcName),
				logger.ErrorField(err))
		}
	}

	logger.Info("Service status collection complete",
		logger.IntField("service_count", len(discovered)))
}
