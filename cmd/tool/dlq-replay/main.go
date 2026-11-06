// Command dlq-replay 把死信队列里的消息按原样回放到源 topic。
//
// 消费者重试耗尽后，消息会被转存到 <topic>.dlq。下游故障恢复之后，这批消息
// 需要一个受控的入口回到正常链路——这个工具就是那个入口。它不猜测内容、
// 不改写负载、不换分区键，只做「按窗口筛选 + 限速投回 + 成功才推进位点」。
//
// 典型用法：
//
//	# 1. 先看清规模，默认 dry-run，一条都不会投出去
//	go run ./cmd/tool/dlq-replay -source-topic chat_messages
//
//	# 2. 只回放故障窗口内的消息，限量并限速
//	go run ./cmd/tool/dlq-replay -source-topic chat_messages \
//	    -since 2026-11-05T02:00:00Z -until 2026-11-05T03:00:00Z \
//	    -limit 200 -rate 20 -dry-run=false -yes
//
// 退出码：0 表示本次账目自洽（无失败）；1 表示存在投回/隔离失败或中途取消；
// 2 表示参数或前置条件不满足，一条都没有动过。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

const exitUsageError = 2

func main() {
	var (
		dlqTopic    = flag.String("dlq-topic", "", "死信 topic（如 chat_messages.dlq）；留空则由 -source-topic 推导")
		sourceTopic = flag.String("source-topic", "", "只回放源 topic 等于该值的死信；留空表示不做源过滤")
		group       = flag.String("group", "", "读死信 topic 的消费组，决定上次回放到哪；留空用 dead-letter-replay")
		since       = flag.String("since", "", "故障窗口起点（含），RFC3339，如 2026-11-05T02:00:00Z")
		until       = flag.String("until", "", "故障窗口终点（不含），RFC3339；缺省表示不设上界")
		limit       = flag.Int("limit", 100, "本次最多实际回放多少条；0 表示不限量，负数等同 0")
		rate        = flag.Int("rate", 0, "每秒回放条数上限；0 用默认 50，负数表示不限速")
		maxReplay   = flag.Int("max-replay-count", 0, "单条消息累计回放轮次上限，超过保留在死信里交人工；0 用默认 3，负数放开")
		dryRun      = flag.Bool("dry-run", true, "只统计不投递；要真正回放需同时给出 -dry-run=false 与 -yes")
		confirmed   = flag.Bool("yes", false, "确认执行真实回放")
		timeout     = flag.Duration("timeout", 2*time.Minute, "整轮回放的时长上限，到点退出并交出已完成账目")
		asJSON      = flag.Bool("json", false, "以 JSON 输出账目，便于脚本与告警消费")
	)
	flag.Parse()

	logger.InitLogger()

	topic := strings.TrimSpace(*dlqTopic)
	if topic == "" {
		src := strings.TrimSpace(*sourceTopic)
		if src == "" {
			usageFail("必须指定 -dlq-topic，或指定 -source-topic 由它推导 <topic>.dlq")
		}
		topic = src + ".dlq"
	}

	sinceAt, untilAt, err := parseWindow(*since, *until)
	if err != nil {
		usageFail(err.Error())
	}

	// 真实投递会把历史消息打回生产 topic，必须显式确认；默认 dry-run 让人先看规模。
	if !*dryRun && !*confirmed {
		usageFail("非 dry-run 回放会向生产 topic 投递消息，请同时给出 -dry-run=false 与 -yes")
	}

	producer := mq.NewProducer()
	if producer == nil {
		usageFail("Kafka 生产者不可用（broker 未就绪或配置缺失），本次未回放任何消息")
	}

	cfg := mq.ReplayConfig{
		DLQTopic:       topic,
		GroupID:        strings.TrimSpace(*group),
		SourceTopic:    strings.TrimSpace(*sourceTopic),
		Limit:          *limit,
		Since:          sinceAt,
		Until:          untilAt,
		MaxReplayCount: *maxReplay,
		RatePerSecond:  *rate,
		DryRun:         *dryRun,
	}

	replayer, err := mq.NewDeadLetterReplayer(cfg, producer)
	if err != nil {
		usageFail("初始化死信回放器失败: " + err.Error())
	}
	defer replayer.Close()

	// Ctrl-C 也要拿到账目：回放做到一半时，运维最需要知道已经放回多少条。
	base, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(base, *timeout)
	defer cancel()

	report, replayErr := replayer.Replay(ctx)
	if report == nil {
		report = &mq.ReplayReport{}
	}

	if jsonErr := printReport(report, cfg.DryRun, *asJSON, replayErr, ctx); jsonErr != nil {
		fmt.Fprintln(os.Stderr, "输出账目失败:", jsonErr)
		os.Exit(1)
	}

	if replayErr != nil || report.Failed > 0 {
		os.Exit(1)
	}
}

// parseWindow 解析故障窗口。两个边界都可选，但给了就必须能解析且顺序正确。
func parseWindow(rawSince, rawUntil string) (time.Time, time.Time, error) {
	var sinceAt, untilAt time.Time
	var err error

	if s := strings.TrimSpace(rawSince); s != "" {
		sinceAt, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("-since 不是 RFC3339 时间: %w", err)
		}
	}
	if s := strings.TrimSpace(rawUntil); s != "" {
		untilAt, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("-until 不是 RFC3339 时间: %w", err)
		}
	}
	if !sinceAt.IsZero() && !untilAt.IsZero() && !untilAt.After(sinceAt) {
		return time.Time{}, time.Time{}, fmt.Errorf("-until 必须晚于 -since，否则窗口内不可能有消息")
	}
	return sinceAt, untilAt, nil
}

func printReport(report *mq.ReplayReport, dryRun bool, asJSON bool, replayErr error, ctx context.Context) error {
	if asJSON {
		payload := struct {
			DryRun   bool   `json:"dry_run"`
			Err      string `json:"error,omitempty"`
			Canceled bool   `json:"canceled"`
			*mq.ReplayReport
		}{DryRun: dryRun, ReplayReport: report, Canceled: ctx.Err() != nil}
		if replayErr != nil {
			payload.Err = replayErr.Error()
		}
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}

	fmt.Println("死信回放账目")
	fmt.Printf("  扫描:      %d\n", report.Scanned)
	if dryRun {
		fmt.Printf("  可回放:    %d (dry-run，未投递)\n", report.Eligible)
	} else {
		fmt.Printf("  已回放:    %d\n", report.Replayed)
	}
	if len(report.ByTopic) > 0 {
		for topic, count := range report.ByTopic {
			fmt.Printf("    - %s: %d\n", topic, count)
		}
	}
	fmt.Printf("  跳过-源topic: %d\n", report.SkippedSource)
	fmt.Printf("  跳过-窗口外:  %d\n", report.SkippedWindow)
	fmt.Printf("  跳过-无时刻:  %d\n", report.SkippedUnknownTime)
	fmt.Printf("  跳过-轮次超限: %d\n", report.SkippedReplayLimit)
	fmt.Printf("  已隔离:      %d\n", report.Quarantined)
	fmt.Printf("  失败:        %d\n", report.Failed)
	fmt.Printf("  位点提交失败: %d\n", report.CommitErrors)
	fmt.Printf("  拉取错误:    %d\n", report.FetchErrors)
	if report.StopReason != "" {
		fmt.Printf("  停止原因:    %s\n", report.StopReason)
	}

	// 账目里每个「没动」的数字都要能解释，否则运维无法判断还剩多少要处理。
	for _, hint := range replayHints(report, dryRun) {
		fmt.Println("  提示:", hint)
	}
	if replayErr != nil {
		fmt.Fprintln(os.Stderr, "  回放中止:", replayErr)
	}
	return nil
}

// replayHints 把计数器翻译成下一步动作。
func replayHints(report *mq.ReplayReport, dryRun bool) []string {
	var hints []string

	if !dryRun && report.CommitErrors > 0 {
		hints = append(hints, "位点提交失败的条目仍留在死信里，下次运行可能重复回放；下游按原始 message_id 主键判重，不会产生重复消息")
	}
	if report.SkippedUnknownTime > 0 {
		hints = append(hints, "这些死信记录没有进入死信的时刻（早于该字段上线），无法按窗口判断，只能去掉 -since/-until 或人工处理")
	}
	if report.SkippedReplayLimit > 0 {
		hints = append(hints, "回放轮次已达上限：同一条消息反复失败说明不是下游抖动，需先看原始 error 再决定是否放开 -max-replay-count")
	}
	if report.Failed > 0 {
		hints = append(hints, "存在投回或隔离失败的条目，对应死信位点未提交，修复下游后重跑即可继续")
	}
	if report.Quarantined > 0 {
		hints = append(hints, "损坏的死信记录已转入 <dlq>.quarantine，原始字节与分区/位点都保留，可据此还原现场")
	}
	return hints
}

func usageFail(msg string) {
	fmt.Fprintln(os.Stderr, "错误:", msg)
	os.Exit(exitUsageError)
}
