package vlm

import (
	"context"
)

// VLM defines the interface for Vision Language Model operations.
type VLM interface {
	// Predict sends an image with a text prompt to the VLM and returns the generated text.
	Predict(ctx context.Context, imgBytes []byte, prompt string) (string, error)

	GetModelName() string
	GetModelID() string
}

// Config holds the configuration needed to create a VLM instance.
type Config struct {
	Source       string // "local" or "remote"
	ModelName    string
	ModelID      string
	BaseURL      string
	APIKey       string
	Provider     string // "openai", "ollama", "aliyun", etc.
}
