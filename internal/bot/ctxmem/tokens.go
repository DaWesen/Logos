package ctxmem

import "github.com/cloudwego/eino/schema"

// defaultMessageOverhead 每条消息的固定开销（角色标记、分隔符等）。
const defaultMessageOverhead = 50

// TokenEstimator 统一 token 估算口径。
//
// 抽成接口不是为了接真实 tokenizer，而是为了测试能注入固定值，
// 精确构造预算边界；线上默认实现与原有 estimateTokenCount 完全一致。
type TokenEstimator interface {
	Estimate(text string) int
	EstimateMessage(msg *schema.Message) int
}

// DefaultEstimator 沿用线上原有算法：ASCII 约 4 字符/token，CJK 约 1 字符/0.6 token，
// 每条消息再加 50 固定开销。
type DefaultEstimator struct{}

func (DefaultEstimator) Estimate(text string) int {
	return estimateRaw(text) + defaultMessageOverhead
}

func (DefaultEstimator) EstimateMessage(msg *schema.Message) int {
	if msg == nil {
		return 0
	}
	total := estimateRaw(msg.Content)
	// 工具调用的函数名与参数同样占用输入 token。旧版只算 Content，
	// 会在工具轮严重低估预算，这是 D6 的修复点。
	for _, call := range msg.ToolCalls {
		total += estimateRaw(call.Function.Name)
		total += estimateRaw(call.Function.Arguments)
	}
	return total + defaultMessageOverhead
}

// EstimateText 是 DefaultEstimator.Estimate 的包级快捷方式。
func EstimateText(text string) int {
	return DefaultEstimator{}.Estimate(text)
}

// EstimateMessages 返回一组消息的估算总量。
func EstimateMessages(msgs []*schema.Message) int {
	est := DefaultEstimator{}
	total := 0
	for _, msg := range msgs {
		total += est.EstimateMessage(msg)
	}
	return total
}

func estimateRaw(text string) int {
	var asciiCount, nonASCIICount int
	for _, r := range text {
		if r < 128 {
			asciiCount++
		} else {
			nonASCIICount++
		}
	}
	return int(float64(asciiCount)*0.25 + float64(nonASCIICount)*0.6)
}
