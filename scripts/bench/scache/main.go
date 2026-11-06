// scache-bench 是语义缓存的本地合成基准。
//
// 它只测量进程内判定与检索成本：不连 Redis、不调用 embedding、不模拟
// 网络，也不代表线上 P99。用途是改索引或判定顺序后，快速确认没有把
// 热路径从微秒级推到毫秒级。
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"Logos/internal/service/ai/bot/service/scache"
	"Logos/pkg/logger"

	"go.uber.org/zap"
)

type options struct {
	entries int
	lookups int
	dim     int
}

func main() {
	// 命中日志会把逐次查询的基准输出淹没，基准只保留汇总结果。
	logger.SetLogger(zap.NewNop())

	opts := parseFlags()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "scache-bench:", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.IntVar(&opts.entries, "entries", 200, "number of entries preloaded into one scope")
	flag.IntVar(&opts.lookups, "lookups", 20000, "lookup iterations per scenario")
	flag.IntVar(&opts.dim, "dim", 128, "embedding dimension")
	flag.Parse()
	return opts
}

func run(opts options) error {
	if opts.entries <= 0 || opts.lookups <= 0 || opts.dim <= 0 {
		return fmt.Errorf("-entries, -lookups and -dim must be positive")
	}

	cfg := scache.DefaultConfig()
	cfg.Mode = scache.ModeOn
	cfg.CalibratedFor = "bench/local"
	cfg.MidThreshold = 0.80
	cfg.HighThreshold = 0.90
	cfg.MaxEntriesPerScope = opts.entries
	cfg.MaxTotalEntries = opts.entries
	cfg.MaxTotalBytes = 1 << 30
	cfg.WriteQueueSize = opts.entries + 64
	cfg.WriteWorkers = 4
	cfg.EvictInterval = time.Hour
	cfg.JudgeEnabled = false
	cfg.ReplayBudget = 0
	cfg.ReplayMaxDelay = 0

	c, err := scache.New(cfg)
	if err != nil {
		return err
	}
	defer c.Close()

	scope := scache.Scope{
		Tenant:     "default",
		BotID:      "bench-bot",
		UserID:     "bench-user",
		KBVersion:  "0",
		PromptHash: scache.PromptHash("bench system prompt"),
		ModelKey:   "bench/model/t=0",
		EmbedKey:   fmt.Sprintf("bench/embed/%d", opts.dim),
	}
	answer := "这是用于本地合成基准的固定答案，不包含真实业务数据，也不代表任何线上指标，只用于比较改动前后的进程内检索耗时。"
	vectors := make([][]float32, opts.entries)
	ctx := context.Background()
	for i := 0; i < opts.entries; i++ {
		vectors[i] = deterministicVector(opts.dim, i)
		c.Store(ctx, scache.StoreRequest{
			Scope:  scope,
			Query:  exactQuery(i),
			Answer: answer,
			Vector: vectors[i],
			Source: "chat",
		})
	}
	if err := waitEntries(c, opts.entries, 30*time.Second); err != nil {
		return err
	}

	exact := measure(opts.lookups, func(i int) scache.LookupResult {
		return c.Lookup(ctx, scache.LookupRequest{
			Scope:  scope,
			Query:  exactQuery(i % opts.entries),
			Vector: vectors[i%opts.entries],
		})
	})
	vector := measure(opts.lookups, func(i int) scache.LookupResult {
		return c.Lookup(ctx, scache.LookupRequest{
			Scope:  scope,
			Query:  paraphraseQuery(i % opts.entries),
			Vector: vectors[i%opts.entries],
		})
	})

	fmt.Println("scache local benchmark (synthetic, no Redis, no embedding)")
	fmt.Printf("entries=%d dim=%d lookups=%d\n", opts.entries, opts.dim, opts.lookups)
	printScenario("exact ", exact)
	printScenario("vector", vector)
	entries, bytes := c.Stats()
	fmt.Printf("local index: entries=%d bytes=%d\n", entries, bytes)
	return nil
}

func exactQuery(i int) string {
	return fmt.Sprintf("基准问题编号%04d：退款政策是什么", i)
}

func paraphraseQuery(i int) string {
	return fmt.Sprintf("请问基准问题编号%04d：退款政策是什么", i)
}

func deterministicVector(dim, seed int) []float32 {
	vec := make([]float32, dim)
	x := uint64(seed+1) * 0x9e3779b97f4a7c15
	var norm float64
	for i := range vec {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		v := float64(int64(x%2001)-1000) / 1000
		vec[i] = float32(v)
		norm += v * v
	}
	if norm == 0 {
		return vec
	}
	scale := 1 / math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) * scale)
	}
	return vec
}

func waitEntries(c *scache.Cache, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n, _ := c.Stats(); n >= want {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	n, _ := c.Stats()
	return fmt.Errorf("preload timed out: entries=%d want=%d", n, want)
}

type result struct {
	decision scache.Decision
	duration []time.Duration
	elapsed  time.Duration
}

func measure(n int, fn func(int) scache.LookupResult) result {
	out := result{duration: make([]time.Duration, 0, n)}
	startAll := time.Now()
	for i := 0; i < n; i++ {
		start := time.Now()
		r := fn(i)
		out.duration = append(out.duration, time.Since(start))
		out.decision = r.Decision
	}
	out.elapsed = time.Since(startAll)
	sort.Slice(out.duration, func(i, j int) bool { return out.duration[i] < out.duration[j] })
	return out
}

func printScenario(name string, r result) {
	if len(r.duration) == 0 {
		fmt.Printf("%s: no samples\n", name)
		return
	}
	mean := time.Duration(0)
	for _, d := range r.duration {
		mean += d
	}
	mean /= time.Duration(len(r.duration))
	last := r.duration[len(r.duration)-1]
	qps := float64(len(r.duration)) / r.elapsed.Seconds()
	fmt.Printf("%s: decision=%s p50=%s p95=%s p99=%s max=%s mean=%s qps=%.0f\n",
		name, r.decision, fmtDuration(percentile(r.duration, 0.50)), fmtDuration(percentile(r.duration, 0.95)),
		fmtDuration(percentile(r.duration, 0.99)), fmtDuration(last), fmtDuration(mean), qps)
}

// fmtDuration 保留亚微秒精度：Windows 计时器分辨率下精确命中常常
// 测到 0，直接按 time.Duration 打印会显示成没有意义的 "0s"。
func fmtDuration(d time.Duration) string {
	if d < time.Microsecond {
		return "<1µs"
	}
	return d.Round(time.Microsecond).String()
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
