package collector

import (
	"strings"
	"testing"
	"time"
)

// TestRetentionDeleteSQL 断言分批删除的关键约束：
// 按 id 子查询 + LIMIT 分批、按时间升序、SKIP LOCKED 支持多副本并发清理。
func TestRetentionDeleteSQL(t *testing.T) {
	query := retentionDeleteSQL("metrics", "timestamp", "")
	upper := strings.ToUpper(query)
	for _, want := range []string{
		"DELETE FROM METRICS",
		"SELECT ID FROM METRICS",
		"TIMESTAMP < ?",
		"ORDER BY TIMESTAMP ASC",
		"LIMIT ?",
		"FOR UPDATE SKIP LOCKED",
	} {
		if !strings.Contains(upper, want) {
			t.Errorf("删除 SQL 缺少 %q: %s", want, query)
		}
	}
}

// TestRetentionDeleteSQL_AlertsOnlyResolved 未解决告警不能删：
// 它们可能仍在排查中，删掉会丢失上下文。
func TestRetentionDeleteSQL_AlertsOnlyResolved(t *testing.T) {
	query := retentionDeleteSQL("alerts", "timestamp", "AND resolved = true")
	if !strings.Contains(query, "AND resolved = true") {
		t.Fatalf("告警清理必须限定已解决: %s", query)
	}
}

// TestRetentionPolicyFromEnv 合法值生效，非法/非正数回退默认，
// 避免一次配置笔误把历史数据全部删光。
func TestRetentionPolicyFromEnv(t *testing.T) {
	t.Setenv("MONITORING_METRICS_RETENTION_DAYS", "3")
	t.Setenv("MONITORING_LOGS_RETENTION_DAYS", "0")
	t.Setenv("MONITORING_ALERTS_RETENTION_DAYS", "abc")

	policy := RetentionPolicyFromEnv()
	if policy.Metrics != 3*24*time.Hour {
		t.Errorf("metrics retention = %s, want 72h", policy.Metrics)
	}
	if policy.Logs != defaultLogsRetention {
		t.Errorf("非法 logs retention 应回退默认，实际 %s", policy.Logs)
	}
	if policy.Alerts != defaultAlertsRetention {
		t.Errorf("非法 alerts retention 应回退默认，实际 %s", policy.Alerts)
	}
}

// TestRetentionDaysFromEnv_Clamps 超长保留期收敛到 10 年，
// 避免 time.Duration 溢出成负数后变成「立刻删除」。
func TestRetentionDaysFromEnv_Clamps(t *testing.T) {
	t.Setenv("MONITORING_METRICS_RETENTION_DAYS", "999999")
	got := retentionDaysFromEnv("MONITORING_METRICS_RETENTION_DAYS", defaultMetricsRetention)
	if got != 3650*24*time.Hour {
		t.Fatalf("保留期 = %s, want 3650 天", got)
	}
	if got <= 0 {
		t.Fatal("保留期溢出成了非正数")
	}
}

// TestDefaultRetentionPolicy 默认值必须为正，否则清理循环不会启动。
func TestDefaultRetentionPolicy(t *testing.T) {
	p := DefaultRetentionPolicy()
	if p.Metrics <= 0 || p.Logs <= 0 || p.Alerts <= 0 {
		t.Fatalf("默认保留策略非法: %+v", p)
	}
}

// TestRetentionCleaner_BatchBounds 单轮必须受批次数限制：
// 积压很多时不能一次删到底，把数据库连接与 WAL 占满。
func TestRetentionCleaner_BatchBounds(t *testing.T) {
	c := NewRetentionCleaner(nil, DefaultRetentionPolicy())
	if c.batchSize <= 0 || c.maxBatchesPerRun <= 0 {
		t.Fatalf("批量参数非法: batch=%d maxBatches=%d", c.batchSize, c.maxBatchesPerRun)
	}
	if c.interval <= 0 {
		t.Fatalf("清理周期必须为正: %s", c.interval)
	}
}
