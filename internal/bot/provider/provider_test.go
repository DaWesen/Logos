package provider

import (
	"testing"

	"github.com/cloudwego/eino/components/model"
)

type stubProvider struct{ name string }

func (s stubProvider) Name() string { return s.name }

func (s stubProvider) NewChatModel(string, string, string) (model.BaseChatModel, error) {
	return nil, nil
}

// TestListProviders_Sorted map 遍历顺序随机，列表必须排序后才能做展示/断言。
func TestListProviders_Sorted(t *testing.T) {
	reg := &ProviderRegistry{providers: map[string]ModelProvider{
		"openai":    stubProvider{"openai"},
		"deepseek":  stubProvider{"deepseek"},
		"platform":  stubProvider{"platform"},
		"chatglm":   stubProvider{"chatglm"},
		"anthropic": stubProvider{"anthropic"},
	}}

	want := []string{"anthropic", "chatglm", "deepseek", "openai", "platform"}
	for i := 0; i < 5; i++ {
		got := reg.ListProviders()
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d", len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("第 %d 次调用顺序不稳定: %v, want %v", i+1, got, want)
			}
		}
	}
}

// TestResolvePlatformConfig_InitializedOverrides 初始化后以全局配置为准，
// 且空 baseURL 必须补默认值（否则空 BaseURL 会被直接交给 SDK）。
func TestResolvePlatformConfig_InitializedOverrides(t *testing.T) {
	inst := &PlatformProvider{apiKey: "global-key", modelName: "global-model", initialized: true}

	apiKey, baseURL, modelName := resolvePlatformConfig(inst, "bot-key", "https://bot.example", "bot-model")
	if apiKey != "global-key" || modelName != "global-model" {
		t.Fatalf("应使用全局配置, got key=%q model=%q", apiKey, modelName)
	}
	if baseURL != defaultPlatformBaseURL {
		t.Fatalf("空 baseURL 应补默认, got %q", baseURL)
	}
}

// TestResolvePlatformConfig_NotInitializedFallback 未初始化时回退调用方参数与默认值。
func TestResolvePlatformConfig_NotInitializedFallback(t *testing.T) {
	apiKey, baseURL, modelName := resolvePlatformConfig(nil, "bot-key", "", "")
	if apiKey != "bot-key" || baseURL != defaultPlatformBaseURL || modelName != defaultPlatformModelName {
		t.Fatalf("got (%q,%q,%q)", apiKey, baseURL, modelName)
	}
}

// TestPlatformProvider_LateInitForRegisteredHandle 注册表在启动早期注册的句柄
// 必须能读到之后才初始化的全局配置；旧实现里该句柄的字段永远是空的，
// 所有 platform 模型构造都报 "api key not configured"。
func TestPlatformProvider_LateInitForRegisteredHandle(t *testing.T) {
	registered := &PlatformProvider{} // 注册表在配置就绪前拿到的空句柄

	InitPlatformProvider("late-key", "", "late-model")

	if _, err := registered.NewChatModel("", "", ""); err != nil {
		t.Fatalf("注册句柄应读到已初始化的全局配置，got %v", err)
	}
}
