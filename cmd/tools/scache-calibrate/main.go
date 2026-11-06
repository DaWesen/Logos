// scache-calibrate 标定语义缓存的 mid/high 阈值。
//
// 这个工具不进生产镜像：它的输入是人工标注的 calib.jsonl，输出是一份
// 可复算的报告。没有跑过真实标定集，就不要往配置里填 calibrated_for。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"Logos/internal/service/ai/bot/service/scache"
	"Logos/internal/service/ai/bot/service/scache/calibrate"
)

type options struct {
	dataset string
	out     string
	baseURL string
	apiKey  string
	model   string
	batch   int
	timeout time.Duration
	min     float64
	max     float64
	step    float64
	check   bool
	mid     float64
	high    float64
	maxFHR  float64
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "scache-calibrate:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	pairs, err := readDataset(opts.dataset)
	if err != nil {
		return err
	}
	if err := calibrate.Validate(pairs); err != nil {
		return err
	}

	samples, dim, err := scorePairs(pairs, opts)
	if err != nil {
		return err
	}

	if opts.check {
		return runCheck(samples, opts, stdout)
	}

	report := calibrate.BuildReport(opts.model, dim, samples, opts.min, opts.max, opts.step)
	printTable(stdout, report)
	if err := writeJSON(opts.out, report); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\n结果已写入 %s\n", opts.out)
	if !report.Selectable {
		return errors.New(report.Note)
	}
	fmt.Fprintf(stdout, "high=%.3f mid=%.3f（把这两个值写入配置后，再跑 -check 复核）\n",
		report.SelectedHigh, report.SelectedMid)
	return nil
}

func parseFlags(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("scache-calibrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&opts.dataset, "dataset", "internal/service/ai/bot/service/scache/testdata/calib.jsonl",
		"标定集路径（JSONL，字段 q1/q2/label[/type]）")
	fs.StringVar(&opts.out, "out", "calib_result.json", "报告输出路径")
	fs.StringVar(&opts.baseURL, "base-url", os.Getenv("SCACHE_EMBED_BASE_URL"),
		"OpenAI 兼容 embeddings 端点，例如 https://api.openai.com/v1")
	fs.StringVar(&opts.apiKey, "api-key", os.Getenv("SCACHE_EMBED_API_KEY"),
		"embeddings API Key（也可用 SCACHE_EMBED_API_KEY）")
	fs.StringVar(&opts.model, "model", os.Getenv("SCACHE_EMBED_MODEL"),
		"embedding 模型名，例如 bge-m3")
	fs.IntVar(&opts.batch, "batch", 32, "单次请求的文本条数")
	fs.DurationVar(&opts.timeout, "timeout", 60*time.Second, "单次 embedding 请求超时")
	fs.Float64Var(&opts.min, "min", 0.80, "扫描下界")
	fs.Float64Var(&opts.max, "max", 0.99, "扫描上界")
	fs.Float64Var(&opts.step, "step", 0.005, "扫描步长")
	fs.BoolVar(&opts.check, "check", false, "只复核配置阈值，假命中率超标时退出码非 0")
	fs.Float64Var(&opts.mid, "mid", 0, "复核用的 mid 阈值")
	fs.Float64Var(&opts.high, "high", 0, "复核用的 high 阈值")
	fs.Float64Var(&opts.maxFHR, "max-fhr", 0.01, "允许的总体假命中率上限")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if opts.check {
		if opts.mid <= 0 || opts.high <= 0 {
			return opts, errors.New("-check 需要同时提供 -mid 与 -high")
		}
		if opts.mid > opts.high {
			return opts, fmt.Errorf("-mid(%v) 不能大于 -high(%v)", opts.mid, opts.high)
		}
	}
	if opts.baseURL == "" || opts.model == "" {
		return opts, errors.New("缺少 embedding 配置：-base-url 与 -model（或对应环境变量）")
	}
	if opts.batch <= 0 {
		return opts, errors.New("-batch 必须大于 0")
	}
	if opts.step <= 0 || opts.max < opts.min {
		return opts, errors.New("扫描区间非法：要求 step>0 且 max>=min")
	}
	return opts, nil
}

func readDataset(path string) ([]calibrate.Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("标定集不存在: %s（请先用真实 query 构造，不要用模型生成全集）", path)
		}
		return nil, err
	}
	defer f.Close()
	return calibrate.LoadJSONL(f)
}

func scorePairs(pairs []calibrate.Pair, opts options) ([]calibrate.Sample, int, error) {
	texts := make([]string, 0, len(pairs)*2)
	seen := make(map[string]bool, len(pairs)*2)
	add := func(text string) {
		if !seen[text] {
			seen[text] = true
			texts = append(texts, text)
		}
	}
	for _, p := range pairs {
		add(p.Q1)
		add(p.Q2)
	}

	client := &embedClient{
		endpoint: strings.TrimRight(opts.baseURL, "/") + "/embeddings",
		apiKey:   opts.apiKey,
		model:    opts.model,
		timeout:  opts.timeout,
		http:     &http.Client{Timeout: opts.timeout},
	}
	vectors := make(map[string][]float32, len(texts))
	for start := 0; start < len(texts); start += opts.batch {
		end := start + opts.batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := client.embed(texts[start:end])
		if err != nil {
			return nil, 0, err
		}
		if len(vecs) != end-start {
			return nil, 0, fmt.Errorf("embedding 返回条数不符: got %d want %d", len(vecs), end-start)
		}
		for i, text := range texts[start:end] {
			vectors[text] = vecs[i]
		}
	}

	dim := 0
	for _, vec := range vectors {
		if dim == 0 {
			dim = len(vec)
		}
		if len(vec) != dim {
			return nil, 0, fmt.Errorf("embedding 维度不一致: %d != %d", len(vec), dim)
		}
	}
	if dim == 0 {
		return nil, 0, errors.New("embedding 返回空向量")
	}

	samples := make([]calibrate.Sample, 0, len(pairs))
	for _, p := range pairs {
		samples = append(samples, calibrate.Sample{
			Pair:       p,
			Similarity: scache.CosineSimilarity(vectors[p.Q1], vectors[p.Q2]),
		})
	}
	return samples, dim, nil
}

func runCheck(samples []calibrate.Sample, opts options, stdout io.Writer) error {
	result := calibrate.Check(samples, opts.high, opts.maxFHR)
	fmt.Fprintf(stdout, "threshold=%.3f  samples=%d  overall_fhr=%.4f%%\n",
		result.Threshold, len(samples), result.OverallFHR*100)

	names := make([]string, 0, len(result.ByType))
	for name := range result.ByType {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stat := result.ByType[name]
		fmt.Fprintf(stdout, "  %-12s total=%-5d false_hits=%-3d fhr=%.4f%%\n",
			name, stat.Total, stat.Hits, stat.FHRate*100)
	}
	if len(result.Violations) > 0 {
		for _, v := range result.Violations {
			fmt.Fprintln(stdout, "FAIL", v)
		}
		return errors.New("标定复核未通过")
	}
	fmt.Fprintf(stdout, "PASS 总体与逐类假命中率均达标（mid=%.3f 仍需判官兜底）\n", opts.mid)
	return nil
}

func printTable(w io.Writer, report calibrate.Report) {
	fmt.Fprintf(w, "embedding=%s (%dd)  samples=%d (hit=%d miss=%d)\n",
		report.Model, report.Dim, report.Samples, report.Positive, report.Negative)
	fmt.Fprintln(w, "  t       hit_rate  fhr       tp   fp   fn   tn")
	for _, row := range report.Rows {
		mark := " "
		if report.Selectable && row.Threshold == report.SelectedHigh {
			mark = "*"
		}
		fmt.Fprintf(w, "%s %.3f   %7.2f%%  %7.2f%%  %-4d %-4d %-4d %-4d\n",
			mark, row.Threshold, row.HitRate*100, row.FalseHitRate*100,
			row.TP, row.FP, row.FN, row.TN)
	}
	fmt.Fprintf(w, "\n正样本 p10=%.4f  负样本 p50=%.4f p90=%.4f p99=%.4f\n",
		report.PositiveP10, report.NegativeP50, report.NegativeP90, report.NegativeP99)
}

func writeJSON(path string, report calibrate.Report) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

type embedClient struct {
	endpoint string
	apiKey   string
	model    string
	timeout  time.Duration
	http     *http.Client
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *embedClient) embed(texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding 返回 %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("embedding 响应解析失败: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("embedding 服务报错: %s", out.Error.Message)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embedding 返回 %d 条，期望 %d", len(out.Data), len(texts))
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })

	vecs := make([][]float32, len(out.Data))
	for i := range out.Data {
		vecs[i] = out.Data[i].Embedding
	}
	return vecs, nil
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
