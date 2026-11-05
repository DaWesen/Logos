package parser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAudioParser_ReturnsTranscript(t *testing.T) {
	parser := NewAudioParser(&fakeASR{text: "会议纪要内容"})

	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("audio"), "meeting.mp3")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if content != "会议纪要内容" {
		t.Fatalf("content = %q, want transcript", content)
	}
	if metadata["transcript"] != "会议纪要内容" {
		t.Fatalf("metadata[transcript] = %v", metadata["transcript"])
	}
	if metadata["type"] != "audio" {
		t.Fatalf("metadata[type] = %v, want audio", metadata["type"])
	}
}

func TestAudioParser_FallsBackWhenTranscribeFails(t *testing.T) {
	parser := NewAudioParser(&fakeASR{err: errors.New("upstream 502")})

	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("audio"), "meeting.mp3")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if !strings.Contains(content, "meeting.mp3") || !strings.Contains(content, "5 字节") {
		t.Fatalf("content = %q, want placeholder with name and size", content)
	}
	if _, ok := metadata["transcript"]; ok {
		t.Fatalf("metadata[transcript] should be absent when transcription fails")
	}
}

func TestAudioParser_PlaceholderWithoutASRModel(t *testing.T) {
	parser := NewAudioParser(nil)

	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("audio"), "demo.wav")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if !strings.Contains(content, "demo.wav") {
		t.Fatalf("content = %q", content)
	}
	if metadata["size"] != 5 {
		t.Fatalf("metadata[size] = %v, want 5", metadata["size"])
	}
}

func TestAudioParser_EmptyTranscriptKeepsPlaceholder(t *testing.T) {
	// 上游 200 但 text 为空：可能是静音音频，此时不应伪造内容，
	// 也不该把空字符串写进 metadata。
	parser := NewAudioParser(&fakeASR{text: "   "})

	_, metadata, err := parser.Parse(context.Background(), strings.NewReader("audio"), "silent.mp3")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if _, ok := metadata["transcript"]; ok {
		t.Fatalf("metadata[transcript] should be absent for blank transcript")
	}
}
