package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/collection"

	"google.golang.org/grpc"
)

type CollectionClient struct {
	client pb.DataCollectionServiceClient
	conn   *grpc.ClientConn
}

func NewCollectionClient(client pb.DataCollectionServiceClient, conn *grpc.ClientConn) *CollectionClient {
	return &CollectionClient{client: client, conn: conn}
}

func NewCollectionClientFromConfig(cfg *config.Config) (*CollectionClient, error) {
	conn, err := newConn(cfg, "logos.collection")
	if err != nil {
		return nil, err
	}
	client := pb.NewDataCollectionServiceClient(conn)
	return NewCollectionClient(client, conn), nil
}

func (c *CollectionClient) CreateTask(ctx context.Context, dataSourceID, name string, format int32, schedule *string) (string, error) {
	resp, err := c.client.CreateTask(ctx, &pb.CreateTaskReq{
		DataSourceId: dataSourceID,
		Name:         name,
		Format:       pb.DataFormat(format),
		Schedule:     schedule,
	})
	if err != nil {
		return "", err
	}
	if resp.Task != nil {
		return resp.Task.Id, nil
	}
	return "", nil
}

func (c *CollectionClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
