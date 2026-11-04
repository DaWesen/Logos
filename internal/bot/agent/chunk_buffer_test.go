package agent

import (
	"errors"
	"strings"
	"testing"
)

// TestChunkBuffer_FirstChunkImmediate 首段必须立即下发：它决定首字延迟。
func TestChunkBuffer_FirstChunkImmediate(t *testing.T) {
	var got []string
	buf := newChunkBuffer(func(s string) error {
		got = append(got, s)
		return nil
	})

	if err := buf.Add("你"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "你" {
		t.Fatalf("首段应立即下发，got %v", got)
	}
}

// TestChunkBuffer_BatchesSmallChunks 后续碎 chunk 合并到阈值再推，
// 帧数应显著少于 chunk 数。
func TestChunkBuffer_BatchesSmallChunks(t *testing.T) {
	var got []string
	buf := newChunkBuffer(func(s string) error {
		got = append(got, s)
		return nil
	})

	piece := strings.Repeat("字", 4) // 每片 4 个字符
	chunks := 30
	for i := 0; i < chunks; i++ {
		if err := buf.Add(piece); err != nil {
			t.Fatal(err)
		}
	}
	if err := buf.Flush(); err != nil {
		t.Fatal(err)
	}

	if len(got) >= chunks {
		t.Fatalf("没有合并，帧数 %d >= chunk 数 %d", len(got), chunks)
	}
	if joined := strings.Join(got, ""); joined != strings.Repeat(piece, chunks) {
		t.Fatalf("合并后内容不完整，长度 %d", len([]rune(joined)))
	}
}

// TestChunkBuffer_FlushRemainder 尾部残余必须推出，否则回答最后一截丢失。
func TestChunkBuffer_FlushRemainder(t *testing.T) {
	var got []string
	buf := newChunkBuffer(func(s string) error {
		got = append(got, s)
		return nil
	})

	_ = buf.Add("前缀")                   // 首段立即推
	_ = buf.Add(strings.Repeat("尾", 3)) // 不足阈值，留在缓冲

	if err := buf.Flush(); err != nil {
		t.Fatal(err)
	}
	if last := got[len(got)-1]; last != strings.Repeat("尾", 3) {
		t.Fatalf("残余未下发: %v", got)
	}
}

// TestChunkBuffer_FlushEmptyIsNoop 没有待推送内容时不应产生空帧。
func TestChunkBuffer_FlushEmptyIsNoop(t *testing.T) {
	calls := 0
	buf := newChunkBuffer(func(string) error {
		calls++
		return nil
	})
	if err := buf.Flush(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("空缓冲不应下发，calls=%d", calls)
	}
}

// TestChunkBuffer_EmitErrorPropagates 下发失败（客户端断开）要立即上抛，
// 不能让 Agent 继续跑完整个回答。
func TestChunkBuffer_EmitErrorPropagates(t *testing.T) {
	want := errors.New("client gone")
	buf := newChunkBuffer(func(string) error { return want })

	if err := buf.Add("首"); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

// TestChunkBuffer_RuneCounting 中文按 rune 计数，不能按字节，
// 否则阈值在中英混合文本里表现不一致。
func TestChunkBuffer_RuneCounting(t *testing.T) {
	emits := 0
	buf := newChunkBuffer(func(string) error {
		emits++
		return nil
	})

	_ = buf.Add("a") // 首段立即推
	for i := 0; i < 10; i++ {
		_ = buf.Add(strings.Repeat("字", 5)) // 累计 50 字符 > 48
	}
	if emits == 0 || emits > 11 {
		t.Fatalf("合并粒度异常, emits=%d", emits)
	}
}
