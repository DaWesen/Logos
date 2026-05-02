package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPTemplateTool struct{}

func (t *HTTPTemplateTool) Name() string        { return "http_request" }
func (t *HTTPTemplateTool) Description() string { return "发送HTTP请求，支持GET/POST/PUT/DELETE方法" }
func (t *HTTPTemplateTool) Type() int           { return 5 }
func (t *HTTPTemplateTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "url", Type: "string", Description: "请求URL", Required: true},
		{Name: "method", Type: "string", Description: "HTTP方法: GET/POST/PUT/DELETE", Required: false, DefaultValue: "GET"},
		{Name: "headers", Type: "string", Description: "请求头JSON，如 {\"Content-Type\":\"application/json\"}", Required: false},
		{Name: "body", Type: "string", Description: "请求体", Required: false},
		{Name: "timeout", Type: "int", Description: "超时时间(秒)", Required: false, DefaultValue: "30"},
	}
}

func (t *HTTPTemplateTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	reqURL := params["url"]
	if reqURL == "" {
		return &ToolResult{Content: "缺少url参数", IsError: true}, nil
	}

	method := params["method"]
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)

	timeoutSec := 30
	if t := params["timeout"]; t != "" {
		fmt.Sscanf(t, "%d", &timeoutSec)
	}

	var body io.Reader
	if params["body"] != "" {
		body = strings.NewReader(params["body"])
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("创建请求失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"url": reqURL, "method": method},
		}, nil
	}

	if headersStr := params["headers"]; headersStr != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(headersStr), &headers); err == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}

	if params["body"] != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("请求失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"url": reqURL, "method": method},
		}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("读取响应失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"url": reqURL, "method": method},
		}, nil
	}

	var respHeaders map[string]string
	respHeaders = make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	headersJSON, _ := json.Marshal(respHeaders)

	var prettyBody bytes.Buffer
	if json.Indent(&prettyBody, respBody, "", "  ") == nil {
		respBody = prettyBody.Bytes()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("状态码: %d %s\n", resp.StatusCode, resp.Status))
	sb.WriteString(fmt.Sprintf("响应大小: %d bytes\n\n", len(respBody)))
	sb.WriteString(string(respBody))

	return &ToolResult{
		Content: sb.String(),
		Metadata: map[string]string{
			"url":         reqURL,
			"method":      method,
			"status_code": fmt.Sprintf("%d", resp.StatusCode),
			"size":        fmt.Sprintf("%d", len(respBody)),
			"headers":     string(headersJSON),
		},
	}, nil
}
