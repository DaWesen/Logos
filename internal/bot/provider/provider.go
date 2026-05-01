package provider

import (
	"context"
	"fmt"
	"sync"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

type ModelProvider interface {
	Name() string
	NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error)
}

type ProviderRegistry struct {
	providers map[string]ModelProvider
	mu        sync.RWMutex
}

var registry *ProviderRegistry
var once sync.Once

func GetProviderRegistry() *ProviderRegistry {
	once.Do(func() {
		registry = &ProviderRegistry{
			providers: make(map[string]ModelProvider),
		}
		registry.RegisterProvider(NewOpenAIProvider())
		registry.RegisterProvider(NewDeepSeekProvider())
		registry.RegisterProvider(NewChatGLMProvider())
		registry.RegisterProvider(NewAnthropicProvider())
		registry.RegisterProvider(NewPlatformProvider())
	})
	return registry
}

func (r *ProviderRegistry) RegisterProvider(p ModelProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

func (r *ProviderRegistry) GetProvider(name string) (ModelProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %s not found", name)
	}
	return p, nil
}

func (r *ProviderRegistry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

type OpenAIProvider struct{}

func NewOpenAIProvider() *OpenAIProvider { return &OpenAIProvider{} }

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
}

type DeepSeekProvider struct{}

func NewDeepSeekProvider() *DeepSeekProvider { return &DeepSeekProvider{} }

func (p *DeepSeekProvider) Name() string { return "deepseek" }

func (p *DeepSeekProvider) NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	if modelName == "" {
		modelName = "deepseek-chat"
	}
	return openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
}

type ChatGLMProvider struct{}

func NewChatGLMProvider() *ChatGLMProvider { return &ChatGLMProvider{} }

func (p *ChatGLMProvider) Name() string { return "chatglm" }

func (p *ChatGLMProvider) NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/paas/v4"
	}
	if modelName == "" {
		modelName = "glm-4"
	}
	return openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
}

type AnthropicProvider struct{}

func NewAnthropicProvider() *AnthropicProvider { return &AnthropicProvider{} }

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	if modelName == "" {
		modelName = "claude-3-opus"
	}
	return openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
}

type PlatformProvider struct{}

func NewPlatformProvider() *PlatformProvider { return &PlatformProvider{} }

func (p *PlatformProvider) Name() string { return "platform" }

func (p *PlatformProvider) NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if modelName == "" {
		modelName = "gpt-4o"
	}
	return openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
}
