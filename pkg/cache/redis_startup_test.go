package cache

import (
	"errors"
	"testing"
	"time"
)

func TestStartupRetryDelay(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 200 * time.Millisecond}, // 非法输入按首次失败处理
		{-3, 200 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1600 * time.Millisecond},
		{5, 2 * time.Second},
		{6, 2 * time.Second},
		{100, 2 * time.Second},
	}
	for _, c := range cases {
		if got := StartupRetryDelay(c.attempt); got != c.want {
			t.Fatalf("attempt=%d: got %s, want %s", c.attempt, got, c.want)
		}
	}
}

func TestStartupRetryDelay_TotalWindowBounded(t *testing.T) {
	// 十次重试的总等待必须远小于旧实现的 150s，否则容器编排会先把进程判死。
	var total time.Duration
	for attempt := 1; attempt < redisStartupAttempts; attempt++ {
		total += StartupRetryDelay(attempt)
	}
	if total > 15*time.Second {
		t.Fatalf("启动重连总等待 %s 过长（应 <= 15s）", total)
	}
}

func TestValidatePipelineCommands(t *testing.T) {
	valid := []PipelineCmd{
		{Cmd: "ZREMRANGEBYSCORE", Args: []interface{}{"k", "0", "100"}},
		{Cmd: "ZADD", Args: []interface{}{"k", 1.0, "member"}},
		{Cmd: "ZRANGE", Args: []interface{}{"k", 0, -1}},
		{Cmd: "PEXPIRE", Args: []interface{}{"k", 1000}},
	}
	if err := ValidatePipelineCommands(valid); err != nil {
		t.Fatalf("合法命令不应报错: %v", err)
	}

	if err := ValidatePipelineCommands(nil); err != nil {
		t.Fatalf("空命令列表不应报错: %v", err)
	}

	// 未知命令必须显式失败，不能被静默跳过
	err := ValidatePipelineCommands([]PipelineCmd{{Cmd: "HSET", Args: []interface{}{"k", "f", "v"}}})
	if !errors.Is(err, ErrUnsupportedPipelineCommand) {
		t.Fatalf("未知命令应返回 ErrUnsupportedPipelineCommand，实际 %v", err)
	}

	// 参数不足曾在旧实现里触发 index out of range panic
	err = ValidatePipelineCommands([]PipelineCmd{{Cmd: "ZADD", Args: []interface{}{"k", 1.0}}})
	if err == nil {
		t.Fatal("参数不足应返回错误")
	}

	err = ValidatePipelineCommands([]PipelineCmd{{Cmd: "PEXPIRE", Args: []interface{}{"k"}}})
	if err == nil {
		t.Fatal("PEXPIRE 缺参应返回错误")
	}

	err = ValidatePipelineCommands([]PipelineCmd{{Cmd: ""}})
	if err == nil {
		t.Fatal("空命令名应返回错误")
	}
}
