package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

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
		// 注册平台提供器（使用 GetPlatformProvider 获取初始化过的实例）
		registry.RegisterProvider(GetPlatformProvider())
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
	// map 遍历顺序随机：不排序的话，同一个进程里两次调用就能给出不同的顺序，
	// 前端下拉/配置对比/测试断言都会随之抖动。
	sort.Strings(names)
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
		Timeout: 120 * time.Second,
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
		Timeout: 120 * time.Second,
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
		Timeout: 120 * time.Second,
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
		Timeout: 120 * time.Second,
	})
}

const (
	defaultPlatformBaseURL   = "https://api.openai.com/v1"
	defaultPlatformModelName = "gpt-4o"
)

type PlatformProvider struct {
	apiKey      string
	baseURL     string
	modelName   string
	initialized bool
}

// platformProviderPtr 已初始化的平台配置。用原子指针而不是普通包变量：
// 读方可能在任意 goroutine 里构造模型，普通变量会构成数据竞争。
var platformProviderPtr atomic.Pointer[PlatformProvider]
var platformProviderOnce sync.Once

func NewPlatformProvider() *PlatformProvider {
	return &PlatformProvider{}
}

func InitPlatformProvider(apiKey, baseURL, modelName string) {
	platformProviderOnce.Do(func() {
		platformProviderPtr.Store(&PlatformProvider{
			apiKey:      apiKey,
			baseURL:     baseURL,
			modelName:   modelName,
			initialized: apiKey != "" && modelName != "",
		})
	})
}

// GetPlatformProvider 返回「当前」平台配置。
//
// 关键在「当前」：注册表在启动早期就会通过 GetProviderRegistry 注册一次
// PlatformProvider，而 InitPlatformProvider 往往在这之后才拿到配置。
// 旧实现把配置存在实例字段上——注册表拿到的是初始化前的空实例，之后所有
// platform 模型构造都读到 initialized=false，永远报 "api key not configured"，
// 与启动顺序强相关且极难复现。因此注册进注册表的实例只作句柄，配置一律在
// 调用时重新读取。
func GetPlatformProvider() *PlatformProvider {
	if p := platformProviderPtr.Load(); p != nil {
		return p
	}
	return &PlatformProvider{
		baseURL:   defaultPlatformBaseURL,
		modelName: defaultPlatformModelName,
	}
}

func (p *PlatformProvider) Name() string { return "platform" }

// resolvePlatformConfig 解析平台模型最终使用的三元组。
// 纯函数便于单测：配置已初始化则以全局配置为准，否则回退调用方参数与默认值。
func resolvePlatformConfig(inst *PlatformProvider, apiKey, baseURL, modelName string) (string, string, string) {
	if inst != nil && inst.initialized {
		apiKey = inst.apiKey
		baseURL = inst.baseURL
		modelName = inst.modelName
	}

	// 初始化时允许不填 baseURL：空值必须补默认，否则会把空 BaseURL 交给 SDK。
	if baseURL == "" {
		baseURL = defaultPlatformBaseURL
	}
	if modelName == "" {
		modelName = defaultPlatformModelName
	}

	return apiKey, baseURL, modelName
}

func (p *PlatformProvider) NewChatModel(apiKey, baseURL, modelName string) (model.BaseChatModel, error) {
	// 使用全局配置而不是 Bot 的配置：在调用时读取，与初始化顺序无关。
	apiKey, baseURL, modelName = resolvePlatformConfig(GetPlatformProvider(), apiKey, baseURL, modelName)

	if apiKey == "" {
		return nil, fmt.Errorf("platform api key not configured, please set in config.yaml")
	}

	return openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
		Timeout: 120 * time.Second,
	})
}
