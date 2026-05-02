package mcp_server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WebSearchTool struct{}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string { return "搜索互联网获取信息，支持多引擎搜索" }
func (t *WebSearchTool) Type() int           { return 1 }
func (t *WebSearchTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "query", Type: "string", Description: "搜索关键词", Required: true},
		{Name: "engine", Type: "string", Description: "搜索引擎: duckduckgo/bing", Required: false, DefaultValue: "duckduckgo"},
		{Name: "count", Type: "int", Description: "返回结果数量", Required: false, DefaultValue: "5"},
	}
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	query := params["query"]
	if query == "" {
		return &ToolResult{Content: "缺少query参数", IsError: true}, nil
	}

	engine := params["engine"]
	if engine == "" {
		engine = "duckduckgo"
	}

	count := 5
	if c := params["count"]; c != "" {
		fmt.Sscanf(c, "%d", &count)
	}

	var results []SearchResult
	var err error

	switch engine {
	case "duckduckgo":
		results, err = searchDuckDuckGo(ctx, query, count)
	default:
		results, err = searchDuckDuckGo(ctx, query, count)
	}

	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("搜索失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"engine": engine, "query": query},
		}, nil
	}

	if len(results) == 0 {
		return &ToolResult{
			Content:  fmt.Sprintf("未找到与 \"%s\" 相关的结果", query),
			Metadata: map[string]string{"engine": engine, "query": query, "result_count": "0"},
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("搜索 \"%s\" 的结果 (共%d条):\n\n", query, len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.Snippet, r.URL))
	}

	resultJSON, _ := json.Marshal(results)
	return &ToolResult{
		Content: sb.String(),
		Metadata: map[string]string{
			"engine":       engine,
			"query":        query,
			"result_count": fmt.Sprintf("%d", len(results)),
			"results_json": string(resultJSON),
		},
	}, nil
}

func searchDuckDuckGo(ctx context.Context, query string, count int) ([]SearchResult, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Logos-AIM-MCP/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var ddgResp struct {
		Abstract      string `json:"Abstract"`
		AbstractURL   string `json:"AbstractURL"`
		AbstractTitle string `json:"AbstractTitle"`
		RelatedTopics []struct {
			Text string `json:"Text"`
			URL  string `json:"FirstURL"`
		} `json:"RelatedTopics"`
		Results []struct {
			Text string `json:"Text"`
			URL  string `json:"FirstURL"`
		} `json:"Results"`
	}

	if err := json.Unmarshal(body, &ddgResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	var results []SearchResult

	if ddgResp.Abstract != "" {
		results = append(results, SearchResult{
			Title:   ddgResp.AbstractTitle,
			URL:     ddgResp.AbstractURL,
			Snippet: ddgResp.Abstract,
		})
	}

	for _, r := range ddgResp.Results {
		if len(results) >= count {
			break
		}
		results = append(results, SearchResult{
			Title:   truncate(r.Text, 80),
			URL:     r.URL,
			Snippet: r.Text,
		})
	}

	for _, r := range ddgResp.RelatedTopics {
		if len(results) >= count {
			break
		}
		if r.Text == "" || r.URL == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   truncate(r.Text, 80),
			URL:     r.URL,
			Snippet: r.Text,
		})
	}

	if len(results) == 0 {
		results = append(results, SearchResult{
			Title:   query,
			URL:     fmt.Sprintf("https://duckduckgo.com/?q=%s", url.QueryEscape(query)),
			Snippet: fmt.Sprintf("请点击链接查看 \"%s\" 的搜索结果", query),
		})
	}

	return results, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
