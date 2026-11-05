package vlm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestVLM(t *testing.T, handler http.HandlerFunc) *RemoteAPIVLM {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "vision-model",
		APIKey:    "test-key",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM() unexpected error: %v", err)
	}
	return client
}

func TestRemoteAPIVLM_PredictSuccess(t *testing.T) {
	client := newTestVLM(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}

		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model != "vision-model" {
			t.Errorf("model = %q", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Errorf("messages = %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "一只猫在窗台上"}},
			},
		})
	})

	text, err := client.Predict(context.Background(), []byte("fake-image"), "描述这张图")
	if err != nil {
		t.Fatalf("Predict() unexpected error: %v", err)
	}
	if text != "一只猫在窗台上" {
		t.Fatalf("Predict() = %q", text)
	}
}

func TestRemoteAPIVLM_AcceptsContentParts(t *testing.T) {
	client := newTestVLM(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role": "assistant",
					"content": []map[string]any{
						{"type": "text", "text": "第一段"},
						{"type": "text", "text": "第二段"},
					},
				}},
			},
		})
	})

	text, err := client.Predict(context.Background(), []byte("fake-image"), "描述")
	if err != nil {
		t.Fatalf("Predict() unexpected error: %v", err)
	}
	if text != "第一段第二段" {
		t.Fatalf("Predict() = %q, want concatenated parts", text)
	}
}

func TestRemoteAPIVLM_EmptyChoicesFails(t *testing.T) {
	client := newTestVLM(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	})

	if _, err := client.Predict(context.Background(), []byte("fake-image"), "描述"); err == nil {
		t.Fatal("Predict() want error when choices is empty")
	}
}

func TestRemoteAPIVLM_EmptyContentFails(t *testing.T) {
	tests := []struct {
		name    string
		content any
	}{
		{"空字符串", ""},
		{"只有空白", "   \n\t "},
		{"空 parts", []any{}},
		{"未知类型", 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestVLM(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{
						{"message": map[string]any{"role": "assistant", "content": tt.content}},
					},
				})
			})

			text, err := client.Predict(context.Background(), []byte("fake-image"), "描述")
			if err == nil {
				t.Fatalf("Predict() = %q, want error for empty/unusable content", text)
			}
		})
	}
}

func TestRemoteAPIVLM_NonOKIncludesBody(t *testing.T) {
	client := newTestVLM(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	})

	_, err := client.Predict(context.Background(), []byte("fake-image"), "描述")
	if err == nil {
		t.Fatal("Predict() want error for non-200 response")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want upstream body included", err)
	}
}

func TestNewRemoteAPIVLM_StripsEndpointSuffix(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
	}{
		{"https://api.example.com/v1", "https://api.example.com/v1"},
		{"https://api.example.com/v1/", "https://api.example.com/v1"},
		{"https://api.example.com/v1/chat/completions", "https://api.example.com/v1"},
		{"https://api.example.com/v1/responses", "https://api.example.com/v1"},
	}

	for _, tt := range tests {
		client, err := NewRemoteAPIVLM(&Config{BaseURL: tt.baseURL, ModelName: "m"})
		if err != nil {
			t.Fatalf("NewRemoteAPIVLM(%q) error: %v", tt.baseURL, err)
		}
		if client.baseURL != tt.want {
			t.Errorf("baseURL = %q, want %q", client.baseURL, tt.want)
		}
	}
}

func TestNewRemoteAPIVLM_RequiresEndpoint(t *testing.T) {
	if _, err := NewRemoteAPIVLM(nil); err == nil {
		t.Fatal("NewRemoteAPIVLM(nil) want error, not panic")
	}
	if _, err := NewRemoteAPIVLM(&Config{}); err == nil {
		t.Fatal("NewRemoteAPIVLM() want error when BaseURL and APIKey are both empty")
	}
}

func TestDetectImageMIME(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}, "image/png"},
		{"jpeg", []byte{0xff, 0xd8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "image/jpeg"},
		{"gif", []byte("GIF89a000000"), "image/gif"},
		{"webp", []byte("RIFF0000WEBP"), "image/webp"},
		{"too short", []byte{0xff, 0xd8}, "image/png"},
	}

	for _, tt := range tests {
		if got := detectImageMIME(tt.data); got != tt.want {
			t.Errorf("detectImageMIME(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
