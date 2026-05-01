package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"Logos/pkg/logger"
)

// OpenAIASR implements ASR via OpenAI-compatible audio transcription API.
type OpenAIASR struct {
	modelName string
	modelID   string
	baseURL   string
	apiKey    string
	client    *http.Client
}

// NewOpenAIASR creates an OpenAI-compatible ASR instance.
func NewOpenAIASR(config *Config) (*OpenAIASR, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	return &OpenAIASR{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		baseURL:   baseURL,
		apiKey:    config.APIKey,
		client: &http.Client{
			Timeout: 90 * time.Second,
		},
	}, nil
}

// Transcribe sends audio bytes to OpenAI Whisper-style API.
func (a *OpenAIASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (string, error) {
	logger.Info("Transcribing audio", 
		logger.StringField("fileName", fileName), 
		logger.IntField("audioSize", len(audioBytes)))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("create form file failed: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(audioBytes)); err != nil {
		return "", fmt.Errorf("write audio data failed: %w", err)
	}

	if err := writer.WriteField("model", a.modelName); err != nil {
		return "", fmt.Errorf("write model field failed: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close writer failed: %w", err)
	}

	url := a.baseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ASR API failed: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse response failed: %w", err)
	}

	return result.Text, nil
}

func (a *OpenAIASR) GetModelName() string {
	return a.modelName
}

func (a *OpenAIASR) GetModelID() string {
	return a.modelID
}
