package ctxmem

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestDefaultEstimator_CountsToolCalls D6：assistant(tool_calls) 的函数名与参数
// 同样进入模型输入。旧实现只算 Content，工具轮会被严重低估。
func TestDefaultEstimator_CountsToolCalls(t *testing.T) {
	est := DefaultEstimator{}
	textOnly := &schema.Message{Role: schema.Assistant, Content: "x"}
	withTool := &schema.Message{
		Role:    schema.Assistant,
		Content: "x",
		ToolCalls: []schema.ToolCall{{
			ID: "call-1",
			Function: schema.FunctionCall{
				Name:      "search_orders",
				Arguments: `{"query":"` + strings.Repeat("a", 400) + `"}`,
			},
		}},
	}

	base := est.EstimateMessage(textOnly)
	got := est.EstimateMessage(withTool)
	if got <= base {
		t.Fatalf("tool call not counted: withTool=%d textOnly=%d", got, base)
	}
	if got < base+90 {
		t.Fatalf("tool call underestimated: withTool=%d, want at least %d", got, base+90)
	}
}

func TestEstimateMessages_SumsEachMessage(t *testing.T) {
	msgs := []*schema.Message{
		schema.UserMessage("hello"),
		schema.AssistantMessage("world", nil),
	}
	want := DefaultEstimator{}.EstimateMessage(msgs[0]) + DefaultEstimator{}.EstimateMessage(msgs[1])
	if got := EstimateMessages(msgs); got != want {
		t.Fatalf("EstimateMessages = %d, want %d", got, want)
	}
}

func TestEstimateMessages_NilMessageIsZero(t *testing.T) {
	if got := EstimateMessages([]*schema.Message{nil}); got != 0 {
		t.Fatalf("nil message = %d, want 0", got)
	}
}
