package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"Logos/pkg/logger"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type MCPToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type mcpClientEntry struct {
	ServiceID     string
	URL           string
	TransportType string
	Headers       map[string]string
	mcpClient     *client.Client
	connected     bool
	initialized   bool
	mu            sync.RWMutex
}

type MCPClientManager struct {
	clients map[string]*mcpClientEntry
	mu      sync.RWMutex
}

func NewMCPClientManager() *MCPClientManager {
	return &MCPClientManager{
		clients: make(map[string]*mcpClientEntry),
	}
}

func (m *MCPClientManager) GetOrCreateConnection(serviceID, url, transportType string, headers map[string]string) *mcpClientEntry {
	m.mu.RLock()
	entry, ok := m.clients[serviceID]
	m.mu.RUnlock()

	if ok {
		entry.mu.Lock()
		needReconnect := entry.URL != url || entry.TransportType != transportType
		entry.URL = url
		entry.TransportType = transportType
		entry.Headers = headers
		entry.mu.Unlock()

		if needReconnect && entry.connected {
			entry.mu.Lock()
			entry.mcpClient.Close()
			entry.connected = false
			entry.initialized = false
			entry.mu.Unlock()
		}
		return entry
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok = m.clients[serviceID]
	if ok {
		entry.mu.Lock()
		needReconnect := entry.URL != url || entry.TransportType != transportType
		entry.URL = url
		entry.TransportType = transportType
		entry.Headers = headers
		entry.mu.Unlock()

		if needReconnect && entry.connected {
			entry.mu.Lock()
			entry.mcpClient.Close()
			entry.connected = false
			entry.initialized = false
			entry.mu.Unlock()
		}
		return entry
	}

	entry = &mcpClientEntry{
		ServiceID:     serviceID,
		URL:           url,
		TransportType: transportType,
		Headers:       headers,
	}
	m.clients[serviceID] = entry
	logger.Info("创建MCP连接",
		logger.StringField("service_id", serviceID),
		logger.StringField("url", url),
		logger.StringField("transport", transportType))
	return entry
}

func (m *MCPClientManager) RemoveConnection(serviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.clients[serviceID]; ok {
		entry.mu.Lock()
		if entry.connected && entry.mcpClient != nil {
			entry.mcpClient.Close()
		}
		entry.mu.Unlock()
		delete(m.clients, serviceID)
	}
	logger.Info("移除MCP连接", logger.StringField("service_id", serviceID))
}

func (m *MCPClientManager) GetConnection(serviceID string) *mcpClientEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[serviceID]
}

func (m *MCPClientManager) ListTools(ctx context.Context, serviceID string) ([]MCPToolInfo, error) {
	entry := m.GetConnection(serviceID)
	if entry == nil {
		return nil, fmt.Errorf("MCP服务连接不存在: %s", serviceID)
	}

	cli, err := m.ensureInitialized(ctx, entry)
	if err != nil {
		return nil, fmt.Errorf("MCP客户端初始化失败: %w", err)
	}

	req := mcp.ListToolsRequest{}
	result, err := cli.ListTools(ctx, req)
	if err != nil {
		m.handleClientError(entry, err)
		return nil, fmt.Errorf("请求工具列表失败: %w", err)
	}

	tools := make([]MCPToolInfo, 0, len(result.Tools))
	for _, tool := range result.Tools {
		inputSchema := make(map[string]interface{})
		inputSchema["type"] = tool.InputSchema.Type
		if tool.InputSchema.Properties != nil {
			inputSchema["properties"] = tool.InputSchema.Properties
		}
		if len(tool.InputSchema.Required) > 0 {
			inputSchema["required"] = tool.InputSchema.Required
		}
		tools = append(tools, MCPToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}

	return tools, nil
}

func (m *MCPClientManager) CallTool(ctx context.Context, serviceID, toolName string, arguments map[string]interface{}) (*CallToolResult, error) {
	entry := m.GetConnection(serviceID)
	if entry == nil {
		return nil, fmt.Errorf("MCP服务连接不存在: %s", serviceID)
	}

	cli, err := m.ensureInitialized(ctx, entry)
	if err != nil {
		return nil, fmt.Errorf("MCP客户端初始化失败: %w", err)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
	}

	result, err := cli.CallTool(ctx, req)
	if err != nil {
		m.handleClientError(entry, err)
		return nil, fmt.Errorf("调用工具失败: %w", err)
	}

	content := make([]ToolContent, 0, len(result.Content))
	for _, item := range result.Content {
		if textContent, ok := mcp.AsTextContent(item); ok {
			content = append(content, ToolContent{
				Type: "text",
				Text: textContent.Text,
			})
		}
	}

	return &CallToolResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

func (m *MCPClientManager) TestConnection(ctx context.Context, url, transportType string, headers map[string]string) error {
	testEntry := &mcpClientEntry{
		URL:           url,
		TransportType: transportType,
		Headers:       headers,
	}

	cli, err := m.createAndInitClient(ctx, testEntry)
	if err != nil {
		return fmt.Errorf("连接测试失败: %w", err)
	}
	cli.Close()
	return nil
}

func (m *MCPClientManager) ensureInitialized(ctx context.Context, entry *mcpClientEntry) (*client.Client, error) {
	entry.mu.RLock()
	if entry.initialized && entry.mcpClient != nil {
		cli := entry.mcpClient
		entry.mu.RUnlock()
		return cli, nil
	}
	entry.mu.RUnlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.initialized && entry.mcpClient != nil {
		return entry.mcpClient, nil
	}

	if entry.connected && entry.mcpClient != nil {
		entry.mcpClient.Close()
		entry.connected = false
		entry.initialized = false
	}

	return m.createAndInitClient(ctx, entry)
}

func (m *MCPClientManager) createAndInitClient(ctx context.Context, entry *mcpClientEntry) (*client.Client, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	headers := make(map[string]string)
	for k, v := range entry.Headers {
		headers[k] = v
	}

	var mcpClient *client.Client
	var err error

	switch entry.TransportType {
	case "sse":
		mcpClient, err = client.NewSSEMCPClient(entry.URL,
			client.WithHTTPClient(httpClient),
			client.WithHeaders(headers),
		)
		if err != nil {
			return nil, fmt.Errorf("创建SSE客户端失败: %w", err)
		}
	case "http-streamable":
		mcpClient, err = client.NewStreamableHttpClient(entry.URL,
			transport.WithHTTPBasicClient(httpClient),
			transport.WithHTTPHeaders(headers),
		)
		if nil != err {
			return nil, fmt.Errorf("创建HTTP Streamable客户端失败: %w", err)
		}
	default:
		mcpClient, err = client.NewStreamableHttpClient(entry.URL,
			transport.WithHTTPBasicClient(httpClient),
			transport.WithHTTPHeaders(headers),
		)
		if err != nil {
			return nil, fmt.Errorf("创建HTTP Streamable客户端失败: %w", err)
		}
	}

	mcpClient.OnConnectionLost(func(err error) {
		entry.mu.Lock()
		entry.connected = false
		entry.initialized = false
		entry.mu.Unlock()
		logger.Warn("MCP服务连接丢失",
			logger.StringField("service_id", entry.ServiceID),
			logger.StringField("url", entry.URL),
			logger.ErrorField(err))
	})

	if err := mcpClient.Start(ctx); err != nil {
		return nil, fmt.Errorf("启动MCP客户端失败: %w", err)
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "Logos",
				Version: "1.0.0",
			},
		},
	}

	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("MCP初始化握手失败: %w", err)
	}

	entry.mcpClient = mcpClient
	entry.connected = true
	entry.initialized = true

	logger.Info("MCP客户端初始化成功",
		logger.StringField("service_id", entry.ServiceID),
		logger.StringField("url", entry.URL),
		logger.StringField("transport", entry.TransportType))

	return mcpClient, nil
}

func (m *MCPClientManager) handleClientError(entry *mcpClientEntry, err error) {
	errMsg := err.Error()
	if strings.Contains(errMsg, "Invalid session ID") ||
		strings.Contains(errMsg, "No active connection") ||
		strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "transport error") {
		entry.mu.Lock()
		if entry.mcpClient != nil {
			entry.mcpClient.Close()
		}
		entry.connected = false
		entry.initialized = false
		entry.mcpClient = nil
		entry.mu.Unlock()

		logger.Warn("MCP客户端连接失效，已断开，将在下次请求时重连",
			logger.StringField("service_id", entry.ServiceID),
			logger.ErrorField(err))
	}
}
