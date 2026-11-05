package parser

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"Logos/internal/models/video"
	"Logos/internal/models/vlm"
)

type fakeVideoExtractor struct {
	info      map[string]interface{}
	infoErr   error
	frames    []*video.Frame
	framesErr error

	infoCalls int
	frameOpts []*video.ExtractOptions
}

func (f *fakeVideoExtractor) ExtractFrames(ctx context.Context, videoBytes []byte, options *video.ExtractOptions) ([]*video.Frame, error) {
	f.frameOpts = append(f.frameOpts, options)
	if f.framesErr != nil {
		return nil, f.framesErr
	}
	return f.frames, nil
}

func (f *fakeVideoExtractor) ExtractFramesToReaders(ctx context.Context, videoBytes []byte, options *video.ExtractOptions) ([]io.Reader, error) {
	frames, err := f.ExtractFrames(ctx, videoBytes, options)
	if err != nil {
		return nil, err
	}
	readers := make([]io.Reader, 0, len(frames))
	for _, frame := range frames {
		readers = append(readers, strings.NewReader(string(frame.ImageData)))
	}
	return readers, nil
}

func (f *fakeVideoExtractor) GetVideoInfo(ctx context.Context, videoBytes []byte) (map[string]interface{}, error) {
	f.infoCalls++
	return f.info, f.infoErr
}

type fakeASR struct {
	text string
	err  error
}

func (f *fakeASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (string, error) {
	return f.text, f.err
}

func (f *fakeASR) GetModelName() string { return "fake-asr" }
func (f *fakeASR) GetModelID() string   { return "fake-asr" }

type fakeVLM struct {
	reply string
	err   error
	calls int
}

func (f *fakeVLM) Predict(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
	f.calls++
	return f.reply, f.err
}

func (f *fakeVLM) GetModelName() string { return "fake-vlm" }
func (f *fakeVLM) GetModelID() string   { return "fake-vlm" }

func TestVideoParser_KeepsASRWhenExtractorFails(t *testing.T) {
	extractor := &fakeVideoExtractor{
		infoErr:   errors.New("ffmpeg: could not find duration"),
		framesErr: errors.New("ffmpeg extraction failed"),
	}
	asrModel := &fakeASR{text: "这是一段讲解视频"}

	parser := NewVideoParser(&fakeVLM{reply: "帧描述"}, asrModel, extractor)
	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("video-bytes"), "demo.mp4")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if !strings.Contains(content, "这是一段讲解视频") {
		t.Fatalf("content = %q, want ASR text preserved when frame extraction fails", content)
	}
	if _, ok := metadata["video_info"]; ok {
		t.Fatalf("metadata[video_info] should be absent when probing fails, got %v", metadata["video_info"])
	}
	if _, ok := metadata["frame_count"]; ok {
		t.Fatalf("metadata[frame_count] should be absent when extraction fails")
	}
}

func TestVideoParser_UsesDurationToPickInterval(t *testing.T) {
	extractor := &fakeVideoExtractor{
		info: map[string]interface{}{"duration": 60.0, "width": 1920, "height": 1080},
		frames: []*video.Frame{
			{Index: 0, Timestamp: 0, ImageData: []byte("frame")},
		},
	}
	vlmModel := &fakeVLM{reply: "画面里有人在演示"}

	parser := NewVideoParser(vlmModel, &fakeASR{}, extractor)
	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("video-bytes"), "demo.mp4")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if len(extractor.frameOpts) != 1 {
		t.Fatalf("ExtractFrames called %d times, want 1", len(extractor.frameOpts))
	}
	// 60s / 6 段 = 10s 一帧，且最多 5 帧。
	if got := extractor.frameOpts[0].FrameInterval; got != 10 {
		t.Fatalf("FrameInterval = %v, want 10", got)
	}
	if got := extractor.frameOpts[0].MaxFrames; got != 5 {
		t.Fatalf("MaxFrames = %v, want 5", got)
	}
	if metadata["video_info"] == nil {
		t.Fatalf("metadata[video_info] should be present when probing succeeds")
	}
	if !strings.Contains(content, "画面里有人在演示") {
		t.Fatalf("content = %q, want frame description included", content)
	}
}

func TestVideoParser_FallsBackWhenVLMFails(t *testing.T) {
	extractor := &fakeVideoExtractor{
		info: map[string]interface{}{"duration": 30.0},
		frames: []*video.Frame{
			{Index: 0, Timestamp: 0, ImageData: []byte("frame-0")},
			{Index: 1, Timestamp: 5, ImageData: []byte("frame-1")},
		},
	}
	vlmModel := &fakeVLM{err: errors.New("upstream 502")}

	parser := NewVideoParser(vlmModel, &fakeASR{}, extractor)
	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("video-bytes"), "demo.mp4")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if vlmModel.calls != 2 {
		t.Fatalf("VLM Predict called %d times, want 2 (one per frame)", vlmModel.calls)
	}
	if _, ok := metadata["frame_count"]; ok {
		t.Fatalf("metadata[frame_count] should be absent when every frame description fails")
	}
	if strings.Contains(content, "【视频帧详情】") {
		t.Fatalf("content = %q, should not render an empty frame section", content)
	}
}

func TestVideoParser_PlaceholderWhenNoModels(t *testing.T) {
	parser := NewVideoParser(nil, nil, nil)
	content, metadata, err := parser.Parse(context.Background(), strings.NewReader("12345"), "demo.mp4")
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if !strings.Contains(content, "demo.mp4") || !strings.Contains(content, "5 字节") {
		t.Fatalf("content = %q, want placeholder with file name and size", content)
	}
	if metadata["type"] != "video" {
		t.Fatalf("metadata[type] = %v, want video", metadata["type"])
	}
}

func TestResolveExtractOptions_ClampsShortVideos(t *testing.T) {
	parser := NewVideoParser(nil, nil, nil)

	opts := parser.resolveExtractOptions(map[string]interface{}{"duration": 3.0})
	if opts.FrameInterval != 1.0 {
		t.Fatalf("FrameInterval = %v, want clamped to 1.0s", opts.FrameInterval)
	}
	if opts.MaxFrames != 5 {
		t.Fatalf("MaxFrames = %v, want 5", opts.MaxFrames)
	}

	// duration 缺失（探测失败）时应回退到默认选项，而不是除以 0。
	fallback := parser.resolveExtractOptions(nil)
	if fallback.Mode != video.ExtractModeInterval || fallback.FrameInterval != 1.0 {
		t.Fatalf("fallback = %+v, want default interval options", fallback)
	}
}

func TestVideoParser_ExplicitOptionsWin(t *testing.T) {
	options := &video.ExtractOptions{Mode: video.ExtractModeKeyFrame, MaxFrames: 2, Format: "jpeg", Quality: 80}
	parser := NewVideoParserWithOptions(nil, nil, nil, options)

	got := parser.resolveExtractOptions(map[string]interface{}{"duration": 600.0})
	if got.Mode != video.ExtractModeKeyFrame || got.MaxFrames != 2 {
		t.Fatalf("resolveExtractOptions() = %+v, want caller-provided options", got)
	}
	if options.MaxFrames != 2 {
		t.Fatalf("caller options mutated: MaxFrames = %d", options.MaxFrames)
	}
}

var _ vlm.VLM = (*fakeVLM)(nil)
