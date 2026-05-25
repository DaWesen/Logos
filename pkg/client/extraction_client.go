package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/extraction"

	"google.golang.org/grpc"
)

type ExtractionClient struct {
	client pb.KnowledgeExtractionServiceClient
	conn   *grpc.ClientConn
}

func NewExtractionClient(client pb.KnowledgeExtractionServiceClient, conn *grpc.ClientConn) *ExtractionClient {
	return &ExtractionClient{client: client, conn: conn}
}

func NewExtractionClientFromConfig(cfg *config.Config) (*ExtractionClient, error) {
	conn, err := newConn(cfg, "logos.extraction")
	if err != nil {
		return nil, err
	}
	client := pb.NewKnowledgeExtractionServiceClient(conn)
	return NewExtractionClient(client, conn), nil
}

func (c *ExtractionClient) CreateTask(ctx context.Context, taskType int32, dataID, dataType string, parameters map[string]string, scheduledTime *string) (string, error) {
	resp, err := c.client.CreateTask(ctx, &pb.CreateExtractionTaskReq{
		Type:          pb.ExtractionTaskType(taskType),
		DataId:        dataID,
		DataType:      dataType,
		Parameters:    parameters,
		ScheduledTime: scheduledTime,
	})
	if err != nil {
		return "", err
	}
	if resp.Task != nil {
		return resp.Task.Id, nil
	}
	return "", nil
}

func (c *ExtractionClient) ExtractFromText(ctx context.Context, text string, taskType int32, parameters map[string]string) (*ExtractionResult, error) {
	resp, err := c.client.ExtractFromText(ctx, &pb.TextExtractionReq{
		Text:       text,
		Type:       pb.ExtractionTaskType(taskType),
		Parameters: parameters,
	})
	if err != nil {
		return nil, err
	}

	result := &ExtractionResult{
		Summary: resp.GetSummary(),
	}
	for _, e := range resp.GetEntities() {
		result.Entities = append(result.Entities, map[string]interface{}{
			"id":   e.GetId(),
			"type": e.GetType(),
			"text": e.GetText(),
		})
	}
	for _, r := range resp.GetRelations() {
		result.Relations = append(result.Relations, map[string]interface{}{
			"id":        r.GetId(),
			"type":      r.GetType(),
			"source_id": r.GetSourceId(),
			"target_id": r.GetTargetId(),
		})
	}
	for _, t := range resp.GetTriples() {
		result.Triples = append(result.Triples, map[string]interface{}{
			"id":          t.GetId(),
			"subject":     t.GetSubject(),
			"predicate":   t.GetPredicate(),
			"object":      t.GetObject(),
			"confidence":  t.GetConfidence(),
		})
	}
	return result, nil
}

func (c *ExtractionClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type ExtractionResult struct {
	Entities  []map[string]interface{}
	Relations []map[string]interface{}
	Triples   []map[string]interface{}
	Summary   string
}
