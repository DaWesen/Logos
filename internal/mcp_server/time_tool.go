package mcp_server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type TimeTool struct{}

func (t *TimeTool) Name() string        { return "time" }
func (t *TimeTool) Description() string { return "获取当前时间、日期，支持时区转换" }
func (t *TimeTool) Type() int           { return 5 }
func (t *TimeTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "action", Type: "string", Description: "操作: now/format/convert", Required: false, DefaultValue: "now"},
		{Name: "timezone", Type: "string", Description: "时区，如 Asia/Shanghai, UTC, America/New_York", Required: false, DefaultValue: "Local"},
		{Name: "format", Type: "string", Description: "时间格式，如 2006-01-02 15:04:05", Required: false, DefaultValue: "2006-01-02 15:04:05"},
		{Name: "timestamp", Type: "int", Description: "Unix时间戳(convert操作时使用)", Required: false},
	}
}

func (t *TimeTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	action := params["action"]
	if action == "" {
		action = "now"
	}

	switch action {
	case "now":
		return t.now(params)
	case "convert":
		return t.convert(params)
	default:
		return t.now(params)
	}
}

func (t *TimeTool) now(params map[string]string) (*ToolResult, error) {
	tz := params["timezone"]
	format := params["format"]
	if format == "" {
		format = "2006-01-02 15:04:05"
	}

	var loc *time.Location
	var err error
	if tz != "" && tz != "Local" {
		loc, err = time.LoadLocation(tz)
		if err != nil {
			return &ToolResult{
				Content:  fmt.Sprintf("无效时区: %s", tz),
				IsError:  true,
				Metadata: map[string]string{"timezone": tz},
			}, nil
		}
	} else {
		loc = time.Local
	}

	now := time.Now().In(loc)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🕐 当前时间: %s\n", now.Format(format)))
	sb.WriteString(fmt.Sprintf("📅 日期: %s\n", now.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("⏰ 时间: %s\n", now.Format("15:04:05")))
	sb.WriteString(fmt.Sprintf("🌍 时区: %s\n", loc.String()))
	sb.WriteString(fmt.Sprintf("📊 Unix时间戳: %d\n", now.Unix()))
	sb.WriteString(fmt.Sprintf("📊 毫秒时间戳: %d\n", now.UnixMilli()))
	sb.WriteString(fmt.Sprintf("📆 星期: %s\n", now.Format("Monday")))

	return &ToolResult{
		Content: sb.String(),
		Metadata: map[string]string{
			"timezone":  loc.String(),
			"timestamp": fmt.Sprintf("%d", now.Unix()),
			"date":      now.Format("2006-01-02"),
			"time":      now.Format("15:04:05"),
		},
	}, nil
}

func (t *TimeTool) convert(params map[string]string) (*ToolResult, error) {
	tsStr := params["timestamp"]
	if tsStr == "" {
		return &ToolResult{Content: "convert操作需要timestamp参数", IsError: true}, nil
	}

	var ts int64
	fmt.Sscanf(tsStr, "%d", &ts)

	tz := params["timezone"]
	format := params["format"]
	if format == "" {
		format = "2006-01-02 15:04:05"
	}

	var loc *time.Location
	var err error
	if tz != "" && tz != "Local" {
		loc, err = time.LoadLocation(tz)
		if err != nil {
			loc = time.Local
		}
	} else {
		loc = time.Local
	}

	var tm time.Time
	if ts > 1e12 {
		tm = time.UnixMilli(ts)
	} else {
		tm = time.Unix(ts, 0)
	}
	tm = tm.In(loc)

	return &ToolResult{
		Content: fmt.Sprintf("时间戳 %d 转换为: %s (时区: %s)", ts, tm.Format(format), loc.String()),
		Metadata: map[string]string{
			"timestamp": tsStr,
			"datetime":  tm.Format(format),
			"timezone":  loc.String(),
		},
	}, nil
}
