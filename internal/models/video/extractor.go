package video

import (
	"context"
	"io"
)

type ExtractMode string

const (
	ExtractModeInterval   ExtractMode = "interval"
	ExtractModeKeyFrame   ExtractMode = "keyframe"
	ExtractModeScene      ExtractMode = "scene"
	ExtractModeTimestamps ExtractMode = "timestamps"
)

type Extractor interface {
	ExtractFrames(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]*Frame, error)
	ExtractFramesToReaders(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]io.Reader, error)
	GetVideoInfo(ctx context.Context, videoBytes []byte) (map[string]interface{}, error)
}

type Frame struct {
	Index     int
	Timestamp float64
	ImageData []byte
	Format    string
	Width     int
	Height    int
}

type ExtractOptions struct {
	Mode ExtractMode `json:"mode"`

	FrameInterval float64 `json:"frame_interval"`
	MaxFrames     int     `json:"max_frames"`
	Format        string  `json:"format"`
	Quality       int     `json:"quality"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`

	SceneThreshold float64 `json:"scene_threshold"`
	Timestamps     []float64 `json:"timestamps"`
}

func DefaultExtractOptions() *ExtractOptions {
	return &ExtractOptions{
		Mode:           ExtractModeInterval,
		FrameInterval:  1.0,
		MaxFrames:      0,
		Format:         "jpeg",
		Quality:        80,
		Width:          0,
		Height:         0,
		SceneThreshold: 0.3,
		Timestamps:     nil,
	}
}

func KeyFrameExtractOptions() *ExtractOptions {
	return &ExtractOptions{
		Mode:      ExtractModeKeyFrame,
		MaxFrames: 0,
		Format:    "jpeg",
		Quality:   80,
		Width:     0,
		Height:    0,
	}
}

func SceneExtractOptions(threshold float64) *ExtractOptions {
	if threshold <= 0 {
		threshold = 0.3
	}
	return &ExtractOptions{
		Mode:            ExtractModeScene,
		SceneThreshold:  threshold,
		MaxFrames:       0,
		Format:          "jpeg",
		Quality:         80,
		Width:           0,
		Height:          0,
	}
}

func TimestampExtractOptions(timestamps []float64) *ExtractOptions {
	return &ExtractOptions{
		Mode:        ExtractModeTimestamps,
		Timestamps:  timestamps,
		MaxFrames:   0,
		Format:      "jpeg",
		Quality:     80,
		Width:       0,
		Height:      0,
	}
}

type Config struct {
	FFMpegPath string
	TempDir    string
}

func NewExtractor(config *Config) (Extractor, error) {
	return NewFFMpegExtractor(config)
}
