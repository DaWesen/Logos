package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"Logos/pkg/logger"
)

const (
	defaultTimeout = 90 * time.Second
	defaultMaxTokens = 5000
	defaultTemperature = 0.1
)

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ChatCompletionRequest struct {
	Model     string     `json:"model"`
	Messages  []Message  `json:"messages"`
	MaxTokens int        `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// RemoteAPIVLM implements VLM via an OpenAI-compatible chat completions API.
type RemoteAPIVLM struct {
	modelName string
	modelID   string
	baseURL   string
	apiKey    string
	client    *http.Client
}

// NewRemoteAPIVLM creates a remote-API backed VLM instance.
func NewRemoteAPIVLM(config *Config) (*RemoteAPIVLM, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	return &RemoteAPIVLM{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		baseURL:   baseURL,
		apiKey:    config.APIKey,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
	}, nil
}

// Predict sends an image with a text prompt to the OpenAI-compatible API.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgData []byte, prompt string) (string, error) {
	mimeType := detectImageMIME(imgData)
	b64 := base64.StdEncoding.EncodeToString(imgData)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)

	reqBody := ChatCompletionRequest{
		Model: v.modelName,
		Messages: []Message{
			{
				Role: "user",
				Content: []map[string]any{
					{
						"type": "image_url",
						"image_url": map[string]any{
							"url": dataURL,
							"detail": "auto",
						},
					},
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		},
		MaxTokens: defaultMaxTokens,
		Temperature: defaultTemperature,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	logger.Info("调用 VLM API", 
		logger.StringField("model", v.modelName), 
		logger.StringField("baseURL", v.baseURL), 
		logger.IntField("imageSize", len(imgData)))

	req, err := http.NewRequestWithContext(ctx, "POST", v.baseURL+"/chat/completions", bytes.NewReader(reqJSON))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := json.MarshalIndent(resp, "", "  ")
		return "", fmt.Errorf("API 返回失败状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var chatResponse ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResponse); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if len(chatResponse.Choices) == 0 {
		return "", fmt.Errorf("响应中没有可用的选项")
	}

	var content string
	switch msgContent := chatResponse.Choices[0].Message.Content.(type) {
	case string:
		content = msgContent
	case []any:
		for _, item := range msgContent {
			if itemMap, ok := item.(map[string]any); ok {
				if text, ok := itemMap["text"].(string); ok {
					content += text
				}
			}
		}
	}

	logger.Info("VLM 响应接收成功", logger.IntField("contentLength", len(content)))
	return content, nil
}

func (v *RemoteAPIVLM) GetModelName() string {
	return v.modelName
}

func (v *RemoteAPIVLM) GetModelID() string {
	return v.modelID
}

func detectImageMIME(data []byte) string {
	if len(data) < 12 {
		return "image/png"
	}

	switch {
	case bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46, 0x38}):
		return "image/gif"
	case bytes.HasPrefix(data, []byte{0x52, 0x49, 0x46, 0x46}):
		return "image/webp"
	}
	return "image/png"
}
