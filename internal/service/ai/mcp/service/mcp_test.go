package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/mcp_server"
	"Logos/internal/service/ai/mcp/model"
)

type fakeMCPRepository struct {
	toolByName      *model.Tool
	toolByNameErr   error
	toolByNameCalls int

	tool    *model.Tool
	toolErr error

	created   []*model.Tool
	createErr error
	updated   []*model.Tool
	updateErr error

	callLogs     []*model.ToolCallLog
	createLogErr error
}

func (f *fakeMCPRepository) CreateTool(_ context.Context, tool *model.Tool) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, tool)
	return nil
}

func (f *fakeMCPRepository) GetTool(_ context.Context, _ string) (*model.Tool, error) {
	return f.tool, f.toolErr
}

func (f *fakeMCPRepository) GetToolByName(_ context.Context, _ string, _ string) (*model.Tool, error) {
	f.toolByNameCalls++
	return f.toolByName, f.toolByNameErr
}

func (f *fakeMCPRepository) ListTools(_ context.Context, _ *int, _ bool, _ string, _, _ int) ([]*model.Tool, int64, error) {
	return nil, 0, nil
}

func (f *fakeMCPRepository) UpdateTool(_ context.Context, tool *model.Tool) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = append(f.updated, tool)
	return nil
}

func (f *fakeMCPRepository) DeleteTool(_ context.Context, _ string) error {
	return nil
}

func (f *fakeMCPRepository) CreateCallLog(_ context.Context, log *model.ToolCallLog) error {
	if f.createLogErr != nil {
		return f.createLogErr
	}
	f.callLogs = append(f.callLogs, log)
	return nil
}

func newTestMCPService(repo *fakeMCPRepository) *mcpServiceImpl {
	return &mcpServiceImpl{
		repo:     repo,
		registry: mcp_server.DefaultRegistry(),
	}
}

func TestRegisterToolLookupFailureIsNotTreatedAsAvailable(t *testing.T) {
	repo := &fakeMCPRepository{toolByNameErr: errors.New("db unavailable")}
	svc := newTestMCPService(repo)

	_, err := svc.RegisterTool(context.Background(), "weather-pro", "", 3, nil, nil, "u1")

	if err == nil || !strings.Contains(err.Error(), "查询工具名称是否重复失败") {
		t.Fatalf("expected lookup error, got %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("tool must not be created when lookup fails, created=%d", len(repo.created))
	}
}

func TestRegisterToolRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		toolType int
		want     string
	}{
		{name: "empty name", toolName: "   ", toolType: 1, want: "名称不能为空"},
		{name: "type zero", toolName: "tool", toolType: 0, want: "有效范围 1-5"},
		{name: "type too large", toolName: "tool", toolType: 6, want: "有效范围 1-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeMCPRepository{}
			svc := newTestMCPService(repo)

			_, err := svc.RegisterTool(context.Background(), tt.toolName, "", tt.toolType, nil, nil, "u1")

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
			if repo.toolByNameCalls != 0 || len(repo.created) != 0 {
				t.Fatalf("invalid input must be rejected before repository access")
			}
		})
	}
}

func TestRegisterToolRejectsDuplicateName(t *testing.T) {
	repo := &fakeMCPRepository{toolByName: &model.Tool{ID: "existing", Name: "dup"}}
	svc := newTestMCPService(repo)

	_, err := svc.RegisterTool(context.Background(), "dup", "", 1, nil, nil, "u1")

	if err == nil || !strings.Contains(err.Error(), "工具名称已存在") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("duplicate name must not be created")
	}
}

func TestRegisterToolPersistsTrimmedNameAndJSON(t *testing.T) {
	repo := &fakeMCPRepository{}
	svc := newTestMCPService(repo)

	tool, err := svc.RegisterTool(context.Background(), "  weather-pro  ", "desc", 3,
		[]ToolParameter{{Name: "city", Type: "string", Required: true}},
		map[string]string{"endpoint": "https://example.com"}, "u1")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.Name != "weather-pro" {
		t.Fatalf("name not trimmed: %q", tool.Name)
	}
	if !strings.Contains(tool.Parameters, `"name":"city"`) {
		t.Fatalf("parameters not serialized: %s", tool.Parameters)
	}
	if !strings.Contains(tool.Config, `"endpoint":"https://example.com"`) {
		t.Fatalf("config not serialized: %s", tool.Config)
	}
	if len(repo.created) != 1 || repo.created[0] != tool {
		t.Fatalf("tool not persisted")
	}
}

func TestUpdateToolRejectsRenameCollision(t *testing.T) {
	repo := &fakeMCPRepository{
		tool:       &model.Tool{ID: "t1", Name: "old", UserID: "u1"},
		toolByName: &model.Tool{ID: "t2", Name: "taken", UserID: "u1"},
	}
	svc := newTestMCPService(repo)

	_, err := svc.UpdateTool(context.Background(), "t1", "taken", "", nil, nil, true)

	if err == nil || !strings.Contains(err.Error(), "工具名称已存在") {
		t.Fatalf("expected rename collision error, got %v", err)
	}
	if len(repo.updated) != 0 {
		t.Fatalf("collision must not be persisted")
	}
}

func TestUpdateToolAllowsKeepingOwnName(t *testing.T) {
	repo := &fakeMCPRepository{
		tool:       &model.Tool{ID: "t1", Name: "same", UserID: "u1"},
		toolByName: &model.Tool{ID: "t1", Name: "same", UserID: "u1"},
	}
	svc := newTestMCPService(repo)

	_, err := svc.UpdateTool(context.Background(), "t1", "same", "", nil, nil, true)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.toolByNameCalls != 0 {
		t.Fatalf("unchanged name should not trigger a duplicate lookup")
	}
	if len(repo.updated) != 1 {
		t.Fatalf("expected one update, got %d", len(repo.updated))
	}
}

func TestUpdateToolLookupFailurePropagates(t *testing.T) {
	repo := &fakeMCPRepository{toolErr: errors.New("db unavailable")}
	svc := newTestMCPService(repo)

	_, err := svc.UpdateTool(context.Background(), "t1", "", "", nil, nil, true)

	if err == nil || !strings.Contains(err.Error(), "查询工具失败") {
		t.Fatalf("expected lookup error, got %v", err)
	}
	if len(repo.updated) != 0 {
		t.Fatalf("update must not run when lookup fails")
	}
}

func TestCallToolCustomUnavailableIsAnError(t *testing.T) {
	repo := &fakeMCPRepository{
		tool: &model.Tool{
			ID:      "t1",
			Name:    "test-custom-tool-unavailable",
			Type:    5,
			Enabled: true,
		},
	}
	svc := newTestMCPService(repo)

	result, metadata, err := svc.CallTool(context.Background(), "t1", "", nil)

	if err == nil || !strings.Contains(err.Error(), "自定义工具执行不可用") {
		t.Fatalf("expected unavailable error, got %v", err)
	}
	if result != "" || metadata != nil {
		t.Fatalf("unavailable tool must not return a fake result, got %q %v", result, metadata)
	}
	if len(repo.callLogs) != 1 || repo.callLogs[0].Status != "error" {
		t.Fatalf("call log must record error status, got %#v", repo.callLogs)
	}
}

func TestCallToolPropagatesLookupFailure(t *testing.T) {
	repo := &fakeMCPRepository{toolErr: errors.New("db unavailable")}
	svc := newTestMCPService(repo)

	_, _, err := svc.CallTool(context.Background(), "t1", "", nil)

	if err == nil || !strings.Contains(err.Error(), "查询工具失败") {
		t.Fatalf("expected lookup error, got %v", err)
	}
}

type fakeMCPServiceRepository struct {
	serviceByName      *model.MCPService
	serviceByNameErr   error
	serviceByNameCalls int

	service    *model.MCPService
	serviceErr error

	created   []*model.MCPService
	createErr error
	updated   []*model.MCPService
	updateErr error
}

func (f *fakeMCPServiceRepository) CreateService(_ context.Context, svc *model.MCPService) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, svc)
	return nil
}

func (f *fakeMCPServiceRepository) GetService(_ context.Context, _ string) (*model.MCPService, error) {
	return f.service, f.serviceErr
}

func (f *fakeMCPServiceRepository) GetServiceByName(_ context.Context, _ string, _ string) (*model.MCPService, error) {
	f.serviceByNameCalls++
	return f.serviceByName, f.serviceByNameErr
}

func (f *fakeMCPServiceRepository) ListServices(_ context.Context, _ bool, _ string, _, _ int) ([]*model.MCPService, int64, error) {
	return nil, 0, nil
}

func (f *fakeMCPServiceRepository) UpdateService(_ context.Context, svc *model.MCPService) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = append(f.updated, svc)
	return nil
}

func (f *fakeMCPServiceRepository) DeleteService(_ context.Context, _ string) error {
	return nil
}

func newTestMCPServiceService(repo *fakeMCPServiceRepository) *mcpServiceServiceImpl {
	return &mcpServiceServiceImpl{svcRepo: repo}
}

func TestCreateServiceLookupFailureIsNotTreatedAsAvailable(t *testing.T) {
	repo := &fakeMCPServiceRepository{serviceByNameErr: errors.New("db unavailable")}
	svc := newTestMCPServiceService(repo)

	_, err := svc.CreateService(context.Background(), "svc", "", "sse", "https://example.com", nil, nil, nil, false, "u1")

	if err == nil || !strings.Contains(err.Error(), "查询MCP服务名称是否重复失败") {
		t.Fatalf("expected lookup error, got %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("service must not be created when lookup fails")
	}
}

func TestCreateServiceRejectsInvalidInput(t *testing.T) {
	repo := &fakeMCPServiceRepository{}
	svc := newTestMCPServiceService(repo)

	if _, err := svc.CreateService(context.Background(), "  ", "", "sse", "https://example.com", nil, nil, nil, false, "u1"); err == nil || !strings.Contains(err.Error(), "名称不能为空") {
		t.Fatalf("expected empty name error, got %v", err)
	}
	if _, err := svc.CreateService(context.Background(), "svc", "", "websocket", "https://example.com", nil, nil, nil, false, "u1"); err == nil || !strings.Contains(err.Error(), "不支持的传输类型") {
		t.Fatalf("expected transport error, got %v", err)
	}
	if repo.serviceByNameCalls != 0 || len(repo.created) != 0 {
		t.Fatalf("invalid input must be rejected before repository access")
	}
}

func TestCreateServiceMergesAuthHeader(t *testing.T) {
	repo := &fakeMCPServiceRepository{}
	svc := newTestMCPServiceService(repo)

	created, err := svc.CreateService(context.Background(), "svc", "", "sse", "https://example.com",
		map[string]string{"X-Trace": "1"},
		map[string]string{"type": "bearer", "token": "secret"},
		nil, false, "u1")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(created.Headers, `"Authorization":"Bearer secret"`) {
		t.Fatalf("authorization header not merged: %s", created.Headers)
	}
	if !strings.Contains(created.Headers, `"X-Trace":"1"`) {
		t.Fatalf("custom header lost: %s", created.Headers)
	}
	if len(repo.created) != 1 {
		t.Fatalf("service not persisted")
	}
}

func TestUpdateServiceRejectsRenameCollision(t *testing.T) {
	repo := &fakeMCPServiceRepository{
		service:          &model.MCPService{ID: "s1", Name: "old", UserID: "u1"},
		serviceByName:    &model.MCPService{ID: "s2", Name: "taken", UserID: "u1"},
		serviceByNameErr: nil,
	}
	svc := newTestMCPServiceService(repo)

	_, err := svc.UpdateService(context.Background(), "s1", "taken", "", "", "", nil, nil, nil, true)

	if err == nil || !strings.Contains(err.Error(), "服务名称已存在") {
		t.Fatalf("expected rename collision error, got %v", err)
	}
	if len(repo.updated) != 0 {
		t.Fatalf("collision must not be persisted")
	}
}
