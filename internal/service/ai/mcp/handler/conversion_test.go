package handler

import (
	"strings"
	"testing"

	"Logos/internal/service/ai/mcp/model"
)

func TestConvertModelToolRejectsCorruptConfig(t *testing.T) {
	_, err := convertModelToolToProtoTool(&model.Tool{ID: "t1", Config: `{"broken"`})

	if err == nil || !strings.Contains(err.Error(), "配置 JSON 损坏") {
		t.Fatalf("expected corrupt config error, got %v", err)
	}
}

func TestConvertModelToolKeepsValidConfig(t *testing.T) {
	tool, err := convertModelToolToProtoTool(&model.Tool{
		ID:     "t1",
		Name:   "weather",
		Type:   3,
		Config: `{"endpoint":"https://example.com"}`,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.Config["endpoint"] != "https://example.com" {
		t.Fatalf("config not decoded: %#v", tool.Config)
	}
}

func TestConvertModelMCPServiceRejectsCorruptField(t *testing.T) {
	tests := []struct {
		name  string
		svc   *model.MCPService
		field string
	}{
		{
			name:  "headers",
			svc:   &model.MCPService{ID: "s1", Headers: `{`},
			field: "headers",
		},
		{
			name:  "auth config",
			svc:   &model.MCPService{ID: "s1", AuthConfig: `not-json`},
			field: "auth_config",
		},
		{
			name:  "advanced config",
			svc:   &model.MCPService{ID: "s1", AdvancedConfig: `[]`},
			field: "advanced_config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := convertModelMCPServiceToProto(tt.svc)

			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("expected %s error, got %v", tt.field, err)
			}
		})
	}
}

func TestConvertModelMCPServiceKeepsValidMaps(t *testing.T) {
	svc, err := convertModelMCPServiceToProto(&model.MCPService{
		ID:             "s1",
		Name:           "svc",
		TransportType:  "sse",
		URL:            "https://example.com",
		Headers:        `{"X-Trace":"1"}`,
		AuthConfig:     `{"type":"bearer"}`,
		AdvancedConfig: `{"timeout":"30s"}`,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.Headers["X-Trace"] != "1" || svc.AuthConfig["type"] != "bearer" || svc.AdvancedConfig["timeout"] != "30s" {
		t.Fatalf("maps not decoded: %#v %#v %#v", svc.Headers, svc.AuthConfig, svc.AdvancedConfig)
	}
}
