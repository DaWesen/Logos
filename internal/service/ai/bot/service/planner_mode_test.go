package service

import (
	"strings"
	"testing"

	"Logos/internal/bot/planner"
	botmodel "Logos/internal/service/ai/bot/model"
)

func TestPlannerMode(t *testing.T) {
	tests := []struct {
		name string
		bot  *botmodel.Bot
		want string
	}{
		{name: "nil bot", want: "legacy"},
		{name: "default mode", bot: &botmodel.Bot{Config: botmodel.JSONMap{}}, want: "legacy"},
		{name: "dag mode", bot: &botmodel.Bot{Config: botmodel.JSONMap{"planner_mode": "dag"}}, want: "dag"},
		{name: "unsupported mode falls back", bot: &botmodel.Bot{Config: botmodel.JSONMap{"planner_mode": "unknown"}}, want: "legacy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := plannerMode(tt.bot); got != tt.want {
				t.Fatalf("plannerMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateResultKeepsUTF8Valid(t *testing.T) {
	output := strings.Repeat("中", 201)
	got := truncateResult([]planner.StepResult{{Output: output}})
	want := strings.Repeat("中", 200) + "..."
	if got != want {
		t.Fatalf("truncateResult() = %q, want %q", got, want)
	}
}
