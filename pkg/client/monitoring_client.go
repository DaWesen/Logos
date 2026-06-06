package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/monitoring"

	"google.golang.org/grpc"
)

type MonitoringClient struct {
	client pb.MonitoringServiceClient
	conn   *grpc.ClientConn
}

func NewMonitoringClient(client pb.MonitoringServiceClient, conn *grpc.ClientConn) *MonitoringClient {
	return &MonitoringClient{client: client, conn: conn}
}

func NewMonitoringClientFromConfig(cfg *config.Config) (*MonitoringClient, error) {
	conn, err := newConn(cfg, "logos.monitoring")
	if err != nil {
		return nil, err
	}
	client := pb.NewMonitoringServiceClient(conn)
	return NewMonitoringClient(client, conn), nil
}

func (c *MonitoringClient) RecordMetric(ctx context.Context, serviceName string, metricType int32, value float64, unit string, tags map[string]string) error {
	_, err := c.client.RecordMetric(ctx, &pb.RecordMetricReq{
		ServiceName: serviceName,
		Type:        pb.MetricType(metricType),
		Value:       value,
		Unit:        unit,
		Tags:        tags,
	})
	return err
}

type MetricInput struct {
	ServiceName string
	Type        int32
	Value       float64
	Unit        string
	Tags        map[string]string
}

func (c *MonitoringClient) BatchRecordMetrics(ctx context.Context, inputs []MetricInput) error {
	reqs := make([]*pb.RecordMetricReq, len(inputs))
	for i, m := range inputs {
		reqs[i] = &pb.RecordMetricReq{
			ServiceName: m.ServiceName,
			Type:        pb.MetricType(m.Type),
			Value:       m.Value,
			Unit:        m.Unit,
			Tags:        m.Tags,
		}
	}
	_, err := c.client.BatchRecordMetric(ctx, &pb.BatchRecordMetricReq{Metrics: reqs})
	return err
}

func (c *MonitoringClient) RecordLog(ctx context.Context, serviceName, level, message string, fields map[string]string) error {
	_, err := c.client.RecordLog(ctx, &pb.RecordLogReq{
		ServiceName: serviceName,
		Level:       level,
		Message:     message,
		Fields:      fields,
	})
	return err
}

func (c *MonitoringClient) UpdateServiceStatus(ctx context.Context, serviceName, status string, errorMessage *string, metadata map[string]string) error {
	_, err := c.client.UpdateServiceStatus(ctx, &pb.ServiceStatus{
		ServiceName:  serviceName,
		Status:       status,
		ErrorMessage: errorMessage,
		Metadata:     metadata,
	})
	return err
}

func (c *MonitoringClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
