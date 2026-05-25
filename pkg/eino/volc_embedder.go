package eino

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/embedding"
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

	results := make([][]float64, 0, len(texts))

	for _, text := range texts {
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

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API error, status: %d, body: %s", resp.StatusCode, string(respBytes))
		}

		var embedResp volcMultimodalEmbedResponse
		if err := json.Unmarshal(respBytes, &embedResp); err != nil {
			return nil, fmt.Errorf("unmarshal response failed: %w", err)
		}

		if embedResp.Error != nil {
			return nil, fmt.Errorf("API error: %s - %s", embedResp.Error.Code, embedResp.Error.Message)
		}

		logger.Info("多模态Embedding成功",
			logger.StringField("model", e.model),
			logger.IntField("dimension", len(embedResp.Data.Embedding)))

		results = append(results, embedResp.Data.Embedding)
	}

	return results, nil
}

func IsMultimodalBaseURL(baseURL string) bool {
	return strings.Contains(baseURL, "/embeddings/multimodal")
}
