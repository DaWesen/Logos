package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"Logos/internal/service/ai/bot/service/scache/calibrate"
)

// fakeEmbedding 把「坏」映射到与正常文本正交的向量，其余文本共享同一向量：
// 正样本相似度恒为 1，负样本恒为 0，标定工具的行为因此完全确定。
func fakeEmbedding(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []item
		for i, text := range req.Input {
			vec := []float32{0, 1}
			if strings.Contains(text, "坏") {
				vec = []float32{1, 0}
			}
			data = append(data, item{Index: i, Embedding: vec})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func writeDataset(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "calib.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_WritesReport(t *testing.T) {
	srv := fakeEmbedding(t)
	defer srv.Close()

	dataset := writeDataset(t, []string{
		`{"q1":"怎么开发票","q2":"发票怎么开","label":"hit"}`,
		`{"q1":"怎么开发票","q2":"坏问题","label":"miss","type":"negation"}`,
	})
	out := filepath.Join(t.TempDir(), "calib_result.json")

	var stdout bytes.Buffer
	err := run([]string{
		"-dataset", dataset, "-out", out,
		"-base-url", srv.URL, "-model", "fake",
		"-min", "0.80", "-max", "0.99", "-step", "0.01",
	}, &stdout)
	if err != nil {
		t.Fatalf("run() error = %v\n%s", err, stdout.String())
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report calibrate.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Selectable || report.SelectedHigh != 0.99 {
		t.Errorf("report = selectable:%v high:%v, want true/0.99", report.Selectable, report.SelectedHigh)
	}
	if report.Samples != 2 || report.Dim != 2 {
		t.Errorf("samples/dim = %d/%d, want 2/2", report.Samples, report.Dim)
	}
}

func TestRun_CheckFailsOnFalseHit(t *testing.T) {
	srv := fakeEmbedding(t)
	defer srv.Close()

	// 负样本的两条问题都被映射到同一向量，sim=1，必然在 high=0.9 假命中。
	dataset := writeDataset(t, []string{
		`{"q1":"怎么开发票","q2":"发票怎么开","label":"hit"}`,
		`{"q1":"正常问题","q2":"另一个正常问题","label":"miss","type":"negation"}`,
	})
	err := run([]string{
		"-dataset", dataset, "-check", "-mid", "0.85", "-high", "0.90",
		"-base-url", srv.URL, "-model", "fake",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(-check) should fail when a hard negative is a false hit")
	}
	if !strings.Contains(err.Error(), "复核未通过") {
		t.Errorf("error = %v, want check failure", err)
	}
}
