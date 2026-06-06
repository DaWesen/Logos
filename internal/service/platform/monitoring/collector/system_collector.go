package collector

import (
	"context"
	"fmt"
	"log"
	"time"

	"Logos/internal/service/platform/monitoring/dao"
	"Logos/internal/service/platform/monitoring/model"
	"Logos/pkg/logger"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"gorm.io/gorm"
)

type SystemCollector struct {
	db       *gorm.DB
	interval time.Duration
}

func NewSystemCollector(db *gorm.DB, interval time.Duration) *SystemCollector {
	return &SystemCollector{
		db:       db,
		interval: interval,
	}
}

func (c *SystemCollector) Start(ctx context.Context) {
	log.Println("[SystemCollector] Starting system metrics collector with interval:", c.interval)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.collect(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[SystemCollector] Stopped")
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *SystemCollector) collect(ctx context.Context) {
	now := time.Now().UnixMilli()
	var metrics []*model.Metric

	if cpuPcts, err := cpu.Percent(0, false); err == nil && len(cpuPcts) > 0 {
		metrics = append(metrics, &model.Metric{
			ID:          uuid.New().String(),
			ServiceName: "system",
			Type:        1,
			Value:       cpuPcts[0],
			Unit:        "%",
			Tags:        dao.MarshalTags(map[string]string{"host": "local"}),
			Timestamp:   now,
		})
	} else if err != nil {
		logger.Error("Failed to collect CPU metric", logger.ErrorField(err))
	}

	if vmStat, err := mem.VirtualMemory(); err == nil {
		metrics = append(metrics, &model.Metric{
			ID:          uuid.New().String(),
			ServiceName: "system",
			Type:        2,
			Value:       vmStat.UsedPercent,
			Unit:        "%",
			Tags:        dao.MarshalTags(map[string]string{
				"host":     "local",
				"total_mb": fmt.Sprintf("%.0f", float64(vmStat.Total)/1024/1024),
				"used_mb":  fmt.Sprintf("%.0f", float64(vmStat.Used)/1024/1024),
			}),
			Timestamp: now,
		})
	} else {
		logger.Error("Failed to collect memory metric", logger.ErrorField(err))
	}

	if diskStat, err := disk.Usage("/"); err == nil {
		metrics = append(metrics, &model.Metric{
			ID:          uuid.New().String(),
			ServiceName: "system",
			Type:        3,
			Value:       diskStat.UsedPercent,
			Unit:        "%",
			Tags:        dao.MarshalTags(map[string]string{
				"host":     "local",
				"total_gb": fmt.Sprintf("%.1f", float64(diskStat.Total)/1024/1024/1024),
				"used_gb":  fmt.Sprintf("%.1f", float64(diskStat.Used)/1024/1024/1024),
			}),
			Timestamp: now,
		})
	} else {
		logger.Error("Failed to collect disk metric", logger.ErrorField(err))
	}

	if ioCounters, err := net.IOCounters(false); err == nil && len(ioCounters) > 0 {
		nic := ioCounters[0]
		metrics = append(metrics, &model.Metric{
			ID:          uuid.New().String(),
			ServiceName: "system",
			Type:        4,
			Value:       float64(nic.BytesSent+nic.BytesRecv) / 1024 / 1024,
			Unit:        "MB",
			Tags:        dao.MarshalTags(map[string]string{
				"host":       "local",
				"bytes_sent": fmt.Sprintf("%d", nic.BytesSent),
				"bytes_recv": fmt.Sprintf("%d", nic.BytesRecv),
			}),
			Timestamp: now,
		})
	} else if err != nil {
		logger.Error("Failed to collect network metric", logger.ErrorField(err))
	}

	if len(metrics) > 0 {
		repo := dao.NewMonitoringRepository(c.db)
		if err := repo.BatchSaveMetrics(ctx, metrics); err != nil {
			logger.Error("Failed to save system metrics", logger.ErrorField(err))
		}
	}

	logger.Info("System metrics collection complete",
		logger.IntField("metric_count", len(metrics)))
}
