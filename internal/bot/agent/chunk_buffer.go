package agent

import (
	"strings"
	"unicode/utf8"
)

// chunkFlushRunes 增量下发的最小合并粒度（按 unicode 字符计）。
//
// 模型逐 token 产出时，每个 chunk 只有 1~3 个字符。逐 chunk 下发意味着
// 一条 500 字回答要发几百帧 WebSocket：前端每帧重渲染、网关每帧写 socket，
// 放大的是网络与 UI 开销，而不是用户感知的速度。
// 合并到约 48 字再推，帧数下降一个数量级；按 rune 而不是 byte 计数，
// 否则中文场景下会在字符中间切开。
const chunkFlushRunes = 48

// chunkBuffer 增量下发的合并缓冲。
//
// 策略：**首段立即推，之后按阈值合并**。
// 首段不能等——它决定用户看到的「首字延迟」；后续碎 chunk 合并，
// 用户在这种粒度下已经感知不到差别。
type chunkBuffer struct {
	pending strings.Builder
	runes   int
	flushed bool
	emit    func(string) error
}

func newChunkBuffer(emit func(string) error) *chunkBuffer {
	return &chunkBuffer{emit: emit}
}

// Add 追加一段增量，满足条件时触发下发。
func (b *chunkBuffer) Add(chunk string) error {
	if chunk == "" {
		return nil
	}
	b.pending.WriteString(chunk)
	b.runes += utf8.RuneCountInString(chunk)

	if !b.flushed || b.runes >= chunkFlushRunes {
		return b.Flush()
	}
	return nil
}

// Flush 把尚未下发的部分推出去；没有待推送内容时为空操作。
func (b *chunkBuffer) Flush() error {
	if b.runes == 0 {
		return nil
	}
	out := b.pending.String()
	b.pending.Reset()
	b.runes = 0
	b.flushed = true
	return b.emit(out)
}
