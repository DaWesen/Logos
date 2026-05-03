package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/mcp"

	"google.golang.org/grpc"
)

type MCPClient struct {
	client pb.MCPServiceClient
	conn   *grpc.ClientConn
}

func NewMCPClient(client pb.MCPServiceClient, conn *grpc.ClientConn) *MCPClient {
	return &MCPClient{client: client, conn: conn}
}

func NewMCPClientFromConfig(cfg *config.Config) (*MCPClient, error) {
	conn, err := newConn(cfg, "logos.mcp")
	if err != nil {
		return nil, err
	}
	client := pb.NewMCPServiceClient(conn)
	return NewMCPClient(client, conn), nil
}

func (c *MCPClient) CallTool(ctx context.Context, toolName string, parameters map[string]string) (string, error) {
	resp, err := c.client.CallTool(ctx, &pb.CallToolRequest{
		ToolName:   toolName,
		Parameters: parameters,
	})
	if err != nil {
		return "", err
	}
	return resp.GetResult(), nil
}

func (c *MCPClient) ListTools(ctx context.Context) ([]*ToolInfo, error) {
	resp, err := c.client.ListTools(ctx, &pb.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	tools := make([]*ToolInfo, 0, len(resp.GetTools()))
	for _, t := range resp.GetTools() {
		tools = append(tools, &ToolInfo{
			ID:          t.Id,
			Name:        t.Name,
			Description: t.Description,
		})
	}
	return tools, nil
}

func (c *MCPClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type ToolInfo struct {
	ID          string
	Name        string
	Description string
}
