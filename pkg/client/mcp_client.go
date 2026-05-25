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

func (c *MCPClient) ListMCPServices(ctx context.Context, enabledOnly bool) ([]*MCPServiceInfo, error) {
	resp, err := c.client.ListMCPServices(ctx, &pb.ListMCPServicesRequest{
		EnabledOnly: enabledOnly,
		Page:        1,
		PageSize:    100,
	})
	if err != nil {
		return nil, err
	}
	services := make([]*MCPServiceInfo, 0, len(resp.GetServices()))
	for _, s := range resp.GetServices() {
		services = append(services, &MCPServiceInfo{
			ID:            s.GetId(),
			Name:          s.GetName(),
			Description:   s.GetDescription(),
			Enabled:       s.GetEnabled(),
			TransportType: s.GetTransportType(),
			URL:           s.GetUrl(),
			Headers:       s.GetHeaders(),
			AuthConfig:    s.GetAuthConfig(),
		})
	}
	return services, nil
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

type MCPServiceInfo struct {
	ID            string
	Name          string
	Description   string
	Enabled       bool
	TransportType string
	URL           string
	Headers       map[string]string
	AuthConfig    map[string]string
}
