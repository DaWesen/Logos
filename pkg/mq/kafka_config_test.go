package mq

import (
	"testing"

	"Logos/config"

	"github.com/segmentio/kafka-go"
)

// TestResolveRequiredAcks 生产端确认级别：缺省最严格（all），显式配置才降级。
func TestResolveRequiredAcks(t *testing.T) {
	cases := []struct {
		name string
		acks string
		want kafka.RequiredAcks
	}{
		{"缺省用 all", "", kafka.RequireAll},
		{"all", "ALL", kafka.RequireAll},
		{"leader", "leader", kafka.RequireOne},
		{"one 别名", "one", kafka.RequireOne},
		{"none", "none", kafka.RequireNone},
		{"未知值回退 all", "whatever", kafka.RequireAll},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRequiredAcks(config.Kafka{RequiredAcks: tc.acks}); got != tc.want {
				t.Fatalf("resolveRequiredAcks(%q) = %v, want %v", tc.acks, got, tc.want)
			}
		})
	}
}

// TestKafkaTopicDefaults topic 分区/副本缺省值：分区默认 6，副本默认 1（开发单 broker）。
func TestKafkaTopicDefaults(t *testing.T) {
	if got := kafkaPartitions(config.Kafka{}); got != 6 {
		t.Fatalf("缺省分区数应为 6，实际 %d", got)
	}
	if got := kafkaReplicationFactor(config.Kafka{}); got != 1 {
		t.Fatalf("缺省副本数应为 1，实际 %d", got)
	}
	if got := kafkaPartitions(config.Kafka{Partitions: 12}); got != 12 {
		t.Fatalf("显式分区数应生效，实际 %d", got)
	}
	if got := kafkaReplicationFactor(config.Kafka{ReplicationFactor: 3}); got != 3 {
		t.Fatalf("显式副本数应生效，实际 %d", got)
	}
}
