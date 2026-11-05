package asr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestASR(t *testing.T, handler http.HandlerFunc) *OpenAIASR {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := NewOpenAIASR(&Config{
		BaseURL:   server.URL,
		ModelName: "whisper-1",
		APIKey:    "test-key",
	})
	if err != nil {
		t.Fatalf("NewOpenAIASR() unexpected error: %v", err)
	}
	return client
}

func TestOpenAIASR_TranscribeSuccess(t *testing.T) {
	client := newTestASR(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("ParseMultipartForm() error: %v", err)
		}
		if got := r.FormValue("model"); got != "whisper-1" {
			t.Errorf("model field = %q, want whisper-1", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "你好，世界"})
	})

	text, err := client.Transcribe(context.Background(), []byte("fake-audio"), "demo.mp3")
	if err != nil {
		t.Fatalf("Transcribe() unexpected error: %v", err)
	}
	if text != "你好，世界" {
		t.Fatalf("Transcribe() = %q, want 你好，世界", text)
	}
}

func TestOpenAIASR_NonOKIncludesBody(t *testing.T) {
	client := newTestASR(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	})

	_, err := client.Transcribe(context.Background(), []byte("fake-audio"), "demo.mp3")
	if err == nil {
		t.Fatal("Transcribe() want error for non-200 response")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %v, want upstream body preserved for troubleshooting", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want status code included", err)
	}
}

func TestOpenAIASR_NonOKWithUnreadableBody(t *testing.T) {
	client := newTestASR(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})

	_, err := client.Transcribe(context.Background(), []byte("fake-audio"), "demo.mp3")
	if err == nil {
		t.Fatal("Transcribe() want error when response body cannot be read")
	}
	if !strings.Contains(err.Error(), "body unreadable") {
		t.Fatalf("error = %v, want read failure surfaced instead of swallowed", err)
	}
}

func TestOpenAIASR_BadJSON(t *testing.T) {
	client := newTestASR(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":`))
	})

	_, err := client.Transcribe(context.Background(), []byte("fake-audio"), "demo.mp3")
	if err == nil {
		t.Fatal("Transcribe() want error for malformed JSON")
	}
}

func TestNewOpenAIASR_RequiresConfig(t *testing.T) {
	if _, err := NewOpenAIASR(nil); err == nil {
		t.Fatal("NewOpenAIASR(nil) want error, not panic")
	}

	if _, err := NewOpenAIASR(&Config{ModelName: ""}); err == nil {
		t.Fatal("NewOpenAIASR() want error when ModelName is empty")
	}
}
