package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newEmbedTestServer 返回一个按输入文本生成向量的假服务，
// 同时记录最大并发数，用于验证批量请求不是串行执行。
func newEmbedTestServer(t *testing.T, delay time.Duration) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var inFlight, maxInFlight atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		var req volcMultimodalEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Input) == 0 {
			http.Error(w, "empty input", http.StatusBadRequest)
			return
		}
		text := req.Input[0].Text
		if strings.Contains(text, "bad") {
			http.Error(w, "upstream failure", http.StatusInternalServerError)
			return
		}

		// 用文本长度作为向量值，方便断言结果与输入一一对应。
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(volcMultimodalEmbedResponse{
			Data: volcMultimodalEmbedData{Embedding: []float64{float64(len(text))}},
		})
	}))

	t.Cleanup(srv.Close)
	return srv, &maxInFlight
}

// TestEmbedStrings_PreservesOrder 并发执行后结果顺序必须与输入一致，
// 否则调用方会把向量和原文错配。
func TestEmbedStrings_PreservesOrder(t *testing.T) {
	srv, _ := newEmbedTestServer(t, 0)
	e, err := NewVolcMultimodalEmbedder("key", "model", srv.URL)
	if err != nil {
		t.Fatalf("创建 embedder 失败: %v", err)
	}

	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	vecs, err := e.EmbedStrings(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedStrings 失败: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("向量数 = %d，期望 %d", len(vecs), len(texts))
	}
	for i, text := range texts {
		if len(vecs[i]) != 1 || vecs[i][0] != float64(len(text)) {
			t.Fatalf("第 %d 条向量 = %v，期望 %d", i, vecs[i], len(text))
		}
	}
}

// TestEmbedStrings_BoundedConcurrency 批量请求必须有并发上限：
// 既不能串行（那样延迟线性叠加），也不能无上限（那样会打爆上游）。
func TestEmbedStrings_BoundedConcurrency(t *testing.T) {
	srv, maxInFlight := newEmbedTestServer(t, 20*time.Millisecond)
	e, err := NewVolcMultimodalEmbedder("key", "model", srv.URL)
	if err != nil {
		t.Fatalf("创建 embedder 失败: %v", err)
	}

	texts := make([]string, 16)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	if _, err := e.EmbedStrings(context.Background(), texts); err != nil {
		t.Fatalf("EmbedStrings 失败: %v", err)
	}

	if got := maxInFlight.Load(); got > multimodalEmbedConcurrency {
		t.Fatalf("最大并发 = %d，超过上限 %d", got, multimodalEmbedConcurrency)
	} else if got < 2 {
		t.Fatalf("最大并发 = %d，批量请求仍是串行执行", got)
	}
}

// TestEmbedStrings_FirstErrorCancels 首错后应停止剩余请求并返回错误，
// 不能把部分结果当成成功返回。
func TestEmbedStrings_FirstErrorCancels(t *testing.T) {
	srv, _ := newEmbedTestServer(t, 0)
	e, err := NewVolcMultimodalEmbedder("key", "model", srv.URL)
	if err != nil {
		t.Fatalf("创建 embedder 失败: %v", err)
	}

	vecs, err := e.EmbedStrings(context.Background(), []string{"ok", "bad", "ok"})
	if err == nil {
		t.Fatal("上游返回 500 时应返回错误")
	}
	if vecs != nil {
		t.Fatalf("失败时不应返回部分结果，实际 %v", vecs)
	}
}

// TestEmbedStrings_CanceledContext 调用方取消时，排队中的请求不应继续发往上游。
func TestEmbedStrings_CanceledContext(t *testing.T) {
	srv, maxInFlight := newEmbedTestServer(t, 50*time.Millisecond)
	e, err := NewVolcMultimodalEmbedder("key", "model", srv.URL)
	if err != nil {
		t.Fatalf("创建 embedder 失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	texts := make([]string, 12)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	if _, err := e.EmbedStrings(ctx, texts); err == nil {
		t.Fatal("ctx 超时应返回错误")
	}
	if got := maxInFlight.Load(); got > multimodalEmbedConcurrency {
		t.Fatalf("取消后仍发出 %d 个并发请求", got)
	}
}

// TestEmbedStrings_EmptyInput 空输入直接报错，不能返回空切片让调用方误判成功。
func TestEmbedStrings_EmptyInput(t *testing.T) {
	e, err := NewVolcMultimodalEmbedder("key", "model", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("创建 embedder 失败: %v", err)
	}
	if _, err := e.EmbedStrings(context.Background(), nil); err == nil {
		t.Fatal("空输入应返回错误")
	}
}

// TestTruncateBody 错误信息必须截断，避免供应商返回的长报文整段进日志。
func TestTruncateBody(t *testing.T) {
	short := "small error"
	if got := truncateBody([]byte(short)); got != short {
		t.Fatalf("短报文不应截断，实际 %q", got)
	}

	long := strings.Repeat("x", 2000)
	got := truncateBody([]byte(long))
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("长报文应带截断标记，实际长度 %d", len(got))
	}
	if len(got) > 600 {
		t.Fatalf("截断后长度 = %d，仍过长", len(got))
	}
}
