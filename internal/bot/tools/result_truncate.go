package tools

// toolResultTruncationMarker 截断标记：让模型明确知道中间有内容被省略，
// 而不是把「头 + 尾」误读成一段连续的文本。
const toolResultTruncationMarker = "\n\n…（中间内容因过长已省略）…\n\n"

// truncateMiddleOfToolResult 保留头部约 60%、尾部约 40%，中间折叠成标记。
//
// 只保留头部是最省事的做法，但工具结果（grep、日志、大 JSON）的关键结论
// 常在末尾：列表合计、错误摘要、最后一条匹配。砍掉尾部等于丢掉最有用的部分。
func truncateMiddleOfToolResult(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	marker := []rune(toolResultTruncationMarker)
	if len(marker) >= limit {
		// 预算小到放不下标记时，退化为纯头部截断。
		return string(runes[:limit])
	}

	keep := limit - len(marker)
	head := keep * 6 / 10
	tail := keep - head

	return string(runes[:head]) + toolResultTruncationMarker + string(runes[len(runes)-tail:])
}
