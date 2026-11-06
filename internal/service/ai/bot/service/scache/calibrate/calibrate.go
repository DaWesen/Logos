// Package calibrate 实现语义缓存阈值标定的纯计算部分。
//
// 这里刻意不做 embedding 调用：标定结果必须能只凭「相似度 + 标注」
// 复算，才能进 CI 做回归（-check），也才能在评审时被独立验证。
// 真正的 embedding 由 cmd/tools/scache-calibrate 负责。
package calibrate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const (
	LabelHit  = "hit"
	LabelMiss = "miss"
)

// Pair 是标定集里的一行：两个问题 + 人工标注。
// Type 用于负样本分类统计（negation/number/entity/time/anaphora/...）。
type Pair struct {
	Q1    string `json:"q1"`
	Q2    string `json:"q2"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
}

// Sample 是 Pair 加上模型算出的余弦相似度。
type Sample struct {
	Pair
	Similarity float64
}

// Row 是一个阈值上的混淆矩阵。
type Row struct {
	Threshold    float64 `json:"threshold"`
	TP           int     `json:"tp"`
	FP           int     `json:"fp"`
	FN           int     `json:"fn"`
	TN           int     `json:"tn"`
	HitRate      float64 `json:"hit_rate"`
	FalseHitRate float64 `json:"false_hit_rate"`
}

// Report 是脚本落盘的完整结果。
type Report struct {
	Model        string          `json:"model"`
	Dim          int             `json:"dim"`
	Samples      int             `json:"samples"`
	Positive     int             `json:"positive"`
	Negative     int             `json:"negative"`
	Min          float64         `json:"min"`
	Max          float64         `json:"max"`
	Step         float64         `json:"step"`
	Rows         []Row           `json:"rows"`
	SelectedHigh float64         `json:"selected_high"`
	SelectedMid  float64         `json:"selected_mid"`
	Selectable   bool            `json:"selectable"`
	PositiveP10  float64         `json:"positive_p10"`
	NegativeP50  float64         `json:"negative_p50"`
	NegativeP90  float64         `json:"negative_p90"`
	NegativeP99  float64         `json:"negative_p99"`
	ByType       map[string]Type `json:"by_type,omitempty"`
	Note         string          `json:"note,omitempty"`
}

// Type 是某一负样本类别在选定 high 阈值上的统计。
type Type struct {
	Total  int     `json:"total"`
	Hits   int     `json:"hits"`
	FHRate float64 `json:"false_hit_rate"`
}

// LoadJSONL 读取标定集。
//
// 空行跳过；label 只接受 hit/miss。任何一行格式错误都直接失败：
// 标定集是「0.90 不是拍脑袋」的唯一证据，静默跳过坏行等于伪造样本量。
func LoadJSONL(r io.Reader) ([]Pair, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var pairs []Pair
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var p Pair
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("第 %d 行 JSON 解析失败: %w", line, err)
		}
		p.Label = strings.ToLower(strings.TrimSpace(p.Label))
		p.Type = strings.ToLower(strings.TrimSpace(p.Type))
		if strings.TrimSpace(p.Q1) == "" || strings.TrimSpace(p.Q2) == "" {
			return nil, fmt.Errorf("第 %d 行缺少 q1/q2", line)
		}
		if p.Label != LabelHit && p.Label != LabelMiss {
			return nil, fmt.Errorf("第 %d 行 label 必须是 hit/miss，实际 %q", line, p.Label)
		}
		if p.Label == LabelMiss && p.Type == "" {
			p.Type = "untyped"
		}
		pairs = append(pairs, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取标定集失败: %w", err)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("标定集为空")
	}
	return pairs, nil
}

// Validate 拒绝只有正样本（或只有负样本）的标定集。
//
// 没有负样本时 FHR 恒为 0，脚本会「成功」选出 0.99；这不是模型好，
// 而是根本没有测假命中。标定集必须同时覆盖两类。
func Validate(pairs []Pair) error {
	var hits, misses int
	for _, p := range pairs {
		switch p.Label {
		case LabelHit:
			hits++
		case LabelMiss:
			misses++
		}
	}
	if hits == 0 || misses == 0 {
		return fmt.Errorf("标定集必须同时包含 hit 与 miss，当前 hit=%d miss=%d", hits, misses)
	}
	return nil
}

// Sweep 在 [min,max] 上按 step 生成阈值表。
//
// 用整数步进而不是 `t += step`：浮点累加会让 0.99 变成 0.9900000001，
// 最后一行被静默丢掉。
func Sweep(samples []Sample, min, max, step float64) []Row {
	if step <= 0 || max < min {
		return nil
	}
	steps := int(math.Floor((max-min)/step+1e-9)) + 1
	rows := make([]Row, 0, steps)
	for i := 0; i < steps; i++ {
		t := min + float64(i)*step
		rows = append(rows, Eval(samples, t))
	}
	return rows
}

// Eval 计算单个阈值上的混淆矩阵。
func Eval(samples []Sample, threshold float64) Row {
	row := Row{Threshold: threshold}
	for _, s := range samples {
		predicted := s.Similarity >= threshold
		switch {
		case s.Label == LabelHit && predicted:
			row.TP++
		case s.Label == LabelHit && !predicted:
			row.FN++
		case s.Label == LabelMiss && predicted:
			row.FP++
		default:
			row.TN++
		}
	}
	row.HitRate = ratio(row.TP, row.TP+row.FN)
	row.FalseHitRate = ratio(row.FP, row.FP+row.TN)
	return row
}

// Select 按 §10.3 选点：
//
//	high = max{ t | FHR(t) < highFHR }
//	mid  = max{ t | FHR(t) < midFHR }
//
// 若 high 候选集为空，说明这套 embedding 不能用于缓存；若 mid 候选集
// 为空，则退化为 mid=high（没有灰区），宁可不省这一次判官调用。
func Select(rows []Row, highFHR, midFHR float64) (mid, high float64, ok bool) {
	highSet := false
	midSet := false
	for _, row := range rows {
		if row.FalseHitRate < highFHR {
			high = row.Threshold
			highSet = true
		}
		if row.FalseHitRate < midFHR {
			mid = row.Threshold
			midSet = true
		}
	}
	if !highSet {
		return 0, 0, false
	}
	if !midSet {
		mid = high
	}
	return mid, high, true
}

// Quantile 返回 p 分位数（p∈[0,1]），线性插值。
func Quantile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	if p <= 0 {
		return cp[0]
	}
	if p >= 1 {
		return cp[len(cp)-1]
	}
	pos := p * float64(len(cp)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return cp[lo]
	}
	weight := pos - float64(lo)
	return cp[lo]*(1-weight) + cp[hi]*weight
}

// ByTypeAt 返回 high 阈值上每个负样本类别的假命中数。
func ByTypeAt(samples []Sample, threshold float64) map[string]Type {
	out := make(map[string]Type)
	for _, s := range samples {
		if s.Label != LabelMiss {
			continue
		}
		stat := out[s.Type]
		stat.Total++
		if s.Similarity >= threshold {
			stat.Hits++
		}
		out[s.Type] = stat
	}
	for name, stat := range out {
		stat.FHRate = ratio(stat.Hits, stat.Total)
		out[name] = stat
	}
	return out
}

// CheckResult 是 -check 模式的判定结果。
type CheckResult struct {
	Threshold  float64         `json:"threshold"`
	OverallFHR float64         `json:"overall_false_hit_rate"`
	ByType     map[string]Type `json:"by_type"`
	Violations []string        `json:"violations,omitempty"`
}

// Check 用配置里的 high 阈值复核标定集。
//
// 除总体 FHR 外，逐类要求硬负样本假命中为 0：总体 1% 可能被
// 1000 条容易样本稀释，而「开启/关闭」错一次就是一次线上事故。
func Check(samples []Sample, high, maxFHR float64) CheckResult {
	row := Eval(samples, high)
	result := CheckResult{
		Threshold:  high,
		OverallFHR: row.FalseHitRate,
		ByType:     ByTypeAt(samples, high),
	}
	if row.FalseHitRate > maxFHR {
		result.Violations = append(result.Violations,
			fmt.Sprintf("总体假命中率 %.2f%% > %.2f%%", row.FalseHitRate*100, maxFHR*100))
	}
	names := make([]string, 0, len(result.ByType))
	for name := range result.ByType {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if stat := result.ByType[name]; stat.Hits > 0 {
			result.Violations = append(result.Violations,
				fmt.Sprintf("类别 %s 假命中 %d/%d", name, stat.Hits, stat.Total))
		}
	}
	return result
}

// BuildReport 汇总一次完整标定。note 由调用方填写「未标定/未压测」等
// 真实状态，禁止在这里补默认数字。
func BuildReport(model string, dim int, samples []Sample, min, max, step float64) Report {
	rows := Sweep(samples, min, max, step)
	mid, high, ok := Select(rows, 0.01, 0.10)

	var pos, neg []float64
	for _, s := range samples {
		if s.Label == LabelHit {
			pos = append(pos, s.Similarity)
		} else {
			neg = append(neg, s.Similarity)
		}
	}

	report := Report{
		Model:        model,
		Dim:          dim,
		Samples:      len(samples),
		Positive:     len(pos),
		Negative:     len(neg),
		Min:          min,
		Max:          max,
		Step:         step,
		Rows:         rows,
		SelectedMid:  mid,
		SelectedHigh: high,
		Selectable:   ok,
		PositiveP10:  Quantile(pos, 0.10),
		NegativeP50:  Quantile(neg, 0.50),
		NegativeP90:  Quantile(neg, 0.90),
		NegativeP99:  Quantile(neg, 0.99),
		ByType:       ByTypeAt(samples, high),
	}
	if !ok {
		report.Note = "没有阈值能把假命中率压到 1% 以下：该 embedding 仅可用于精确命中"
	}
	return report
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}
