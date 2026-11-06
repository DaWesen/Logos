package calibrate

import (
	"math"
	"strings"
	"testing"
)

func TestLoadJSONL(t *testing.T) {
	raw := `{"q1":"怎么关闭自动续费","q2":"自动续费如何取消","label":"HIT"}

{"q1":"怎么关闭自动续费","q2":"怎么开启自动续费","label":"miss","type":"Negation"}
{"q1":"500元能买什么","q2":"1000元能买什么","label":"miss"}
`
	pairs, err := LoadJSONL(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("LoadJSONL() error = %v", err)
	}
	if len(pairs) != 3 {
		t.Fatalf("pairs = %d, want 3", len(pairs))
	}
	if pairs[0].Label != LabelHit || pairs[1].Type != "negation" {
		t.Errorf("label/type normalization wrong: %+v", pairs)
	}
	if pairs[2].Type != "untyped" {
		t.Errorf("miss without type should be untyped, got %q", pairs[2].Type)
	}
}

func TestLoadJSONL_RejectsBadLabel(t *testing.T) {
	_, err := LoadJSONL(strings.NewReader(`{"q1":"a","q2":"b","label":"maybe"}`))
	if err == nil || !strings.Contains(err.Error(), "label") {
		t.Fatalf("expected label error, got %v", err)
	}
}

func TestValidate_RequiresBothLabels(t *testing.T) {
	onlyHits := []Pair{{Label: LabelHit}, {Label: LabelHit}}
	if err := Validate(onlyHits); err == nil {
		t.Fatal("dataset without negatives must be rejected")
	}
	both := []Pair{{Label: LabelHit}, {Label: LabelMiss, Type: "negation"}}
	if err := Validate(both); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSweep_ConfusionMatrix(t *testing.T) {
	samples := []Sample{
		{Pair: Pair{Label: LabelHit}, Similarity: 0.95},
		{Pair: Pair{Label: LabelHit}, Similarity: 0.85},
		{Pair: Pair{Label: LabelMiss, Type: "negation"}, Similarity: 0.95},
		{Pair: Pair{Label: LabelMiss, Type: "number"}, Similarity: 0.50},
	}
	rows := Sweep(samples, 0.90, 0.90, 0.005)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.TP != 1 || got.FN != 1 || got.FP != 1 || got.TN != 1 {
		t.Fatalf("matrix = %+v, want tp=1 fn=1 fp=1 tn=1", got)
	}
	if math.Abs(got.HitRate-0.5) > 1e-9 || math.Abs(got.FalseHitRate-0.5) > 1e-9 {
		t.Errorf("rates = %v/%v, want 0.5/0.5", got.HitRate, got.FalseHitRate)
	}
}

func TestSweep_FloatAccumulationDoesNotDropLastRow(t *testing.T) {
	rows := Sweep(nil, 0.80, 0.99, 0.005)
	if len(rows) != 39 {
		t.Fatalf("rows = %d, want 39", len(rows))
	}
	if math.Abs(rows[len(rows)-1].Threshold-0.99) > 1e-9 {
		t.Errorf("last threshold = %v, want 0.99", rows[len(rows)-1].Threshold)
	}
}

func TestSelect_Rule(t *testing.T) {
	rows := []Row{
		{Threshold: 0.90, FalseHitRate: 0.12},
		{Threshold: 0.91, FalseHitRate: 0.05},
		{Threshold: 0.92, FalseHitRate: 0.02},
		{Threshold: 0.93, FalseHitRate: 0.009},
		{Threshold: 0.94, FalseHitRate: 0.001},
	}
	mid, high, ok := Select(rows, 0.01, 0.10)
	if !ok {
		t.Fatal("Select() should find a threshold")
	}
	// FHR 在 .94 仍低于 10%，所以灰区下界与直接命中上界可以重合。
	if high != 0.94 || mid != 0.94 {
		t.Errorf("mid/high = %v/%v, want 0.94/0.94", mid, high)
	}
}

func TestSelect_NoCandidate(t *testing.T) {
	rows := []Row{{Threshold: 0.90, FalseHitRate: 0.30}}
	if _, _, ok := Select(rows, 0.01, 0.10); ok {
		t.Error("Select() should report no usable threshold")
	}
}

func TestQuantile(t *testing.T) {
	values := []float64{0.9, 0.1, 0.5, 0.3, 0.7}
	if got := Quantile(values, 0); got != 0.1 {
		t.Errorf("p0 = %v, want 0.1", got)
	}
	if got := Quantile(values, 1); got != 0.9 {
		t.Errorf("p100 = %v, want 0.9", got)
	}
	if got := Quantile(values, 0.5); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("p50 = %v, want 0.5", got)
	}
}

func TestCheck_TypeCannotBeDilutedByOverallRate(t *testing.T) {
	samples := make([]Sample, 0, 201)
	for i := 0; i < 200; i++ {
		samples = append(samples, Sample{
			Pair:       Pair{Label: LabelMiss, Type: "entity"},
			Similarity: 0.10,
		})
	}
	samples = append(samples, Sample{
		Pair:       Pair{Label: LabelMiss, Type: "negation"},
		Similarity: 0.99,
	})

	result := Check(samples, 0.95, 0.01)
	if result.OverallFHR > 0.01 {
		t.Fatalf("overall FHR = %v, fixture should keep it under 1%%", result.OverallFHR)
	}
	if len(result.Violations) != 1 || !strings.Contains(result.Violations[0], "negation") {
		t.Fatalf("expected negation-only violation, got %v", result.Violations)
	}
}

func TestBuildReport_UnselectableModel(t *testing.T) {
	samples := []Sample{
		{Pair: Pair{Label: LabelHit}, Similarity: 0.99},
		{Pair: Pair{Label: LabelMiss, Type: "negation"}, Similarity: 0.999},
	}
	report := BuildReport("bad-model", 8, samples, 0.90, 0.99, 0.01)
	if report.Selectable {
		t.Fatal("report should mark the model unselectable")
	}
	if report.Note == "" {
		t.Error("unselectable report must explain why")
	}
}
