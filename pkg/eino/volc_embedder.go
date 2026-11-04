package eino

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/embedding"
)

const (
	// 多模态接口一次只接受一条文本，批量只能拆成多次 HTTP 请求。
	// 串行执行时 100 条文本就是 100 次往返叠加；这里用固定并发把总耗时压到
	// 「批次数 / 并发度」，同时给上游一个明确的上限，避免把供应商打限流。
	multimodalEmbedConcurrency = 4
	// 向量响应正常只有几十 KB；设上限是为了防止异常响应把内存吃满。
	maxEmbedResponseBytes = 8 << 20
)

type volcMultimodalEmbedRequest struct {
	Model          string                    `json:"model"`
	EncodingFormat string                    `json:"encoding_format"`
	Input          []volcMultimodalInputItem `json:"input"`
}

type volcMultimodalInputItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type volcMultimodalEmbedResponse struct {
	ID    string                    `json:"id"`
	Model string                    `json:"model"`
	Data  volcMultimodalEmbedData   `json:"data"`
	Usage *volcMultimodalEmbedUsage `json:"usage,omitempty"`
	Error *volcMultimodalEmbedError `json:"error,omitempty"`
}

type volcMultimodalEmbedData struct {
	Embedding []float64 `json:"embedding"`
	Object    string    `json:"object"`
}

type volcMultimodalEmbedUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type volcMultimodalEmbedError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type VolcMultimodalEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func NewVolcMultimodalEmbedder(apiKey, model, baseURL string) (*VolcMultimodalEmbedder, error) {
	if baseURL == "" {
		baseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"
	}

	return &VolcMultimodalEmbedder{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

func (e *VolcMultimodalEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	// 结果按下标写回，保证并发下顺序与输入一致：
	// 调用方按 texts[i] 对应 vectors[i] 使用，乱序会让向量和原文错配。
	results := make([][]float64, len(texts))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, multimodalEmbedConcurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	for i, text := range texts {
		wg.Add(1)
		go func(idx int, input string) {
			defer wg.Done()

			// 占位也要能被取消：上游已经失败时，排队中的 goroutine 不应该继续发请求。
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-runCtx.Done():
				return
			}

			vec, err := e.embedOne(runCtx, input)
			if err != nil {
				// 首错取消：批量接口里继续跑剩余请求只会浪费配额和上游资源。
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			results[idx] = vec
		}(i, text)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for i, vec := range results {
		if len(vec) == 0 {
			return nil, fmt.Errorf("embedding %d is empty", i)
		}
	}
	return results, nil
}

// embedOne 处理单条文本，返回维度已校验的向量。
func (e *VolcMultimodalEmbedder) embedOne(ctx context.Context, text string) ([]float64, error) {
	reqBody := volcMultimodalEmbedRequest{
		Model:          e.model,
		EncodingFormat: "float",
		Input: []volcMultimodalInputItem{
			{Type: "text", Text: text},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// 多读 1 字节用于判断是否超限；超过上限直接失败，不把异常响应读进内存。
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	if len(respBytes) > maxEmbedResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxEmbedResponseBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error, status: %d, body: %s", resp.StatusCode, truncateBody(respBytes))
	}

	var embedResp volcMultimodalEmbedResponse
	if err := json.Unmarshal(respBytes, &embedResp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	if embedResp.Error != nil {
		return nil, fmt.Errorf("API error: %s - %s", embedResp.Error.Code, embedResp.Error.Message)
	}

	if len(embedResp.Data.Embedding) == 0 {
		return nil, fmt.Errorf("API returned empty embedding")
	}

	logger.Info("多模态Embedding成功",
		logger.StringField("model", e.model),
		logger.IntField("dimension", len(embedResp.Data.Embedding)))

	return embedResp.Data.Embedding, nil
}

// truncateBody 限制错误信息长度：供应商返回的 HTML/长报文不应该整段进日志。
func truncateBody(body []byte) string {
	const maxBodyInError = 512
	if len(body) <= maxBodyInError {
		return string(body)
	}
	return string(body[:maxBodyInError]) + "...(truncated)"
}

func IsMultimodalBaseURL(baseURL string) bool {
	return strings.Contains(baseURL, "/embeddings/multimodal")
}
