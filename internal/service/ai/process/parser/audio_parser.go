package parser

import (
	"context"
	"fmt"
	"io"

	"Logos/internal/models/asr"
)

type AudioParser struct {
	asrModel asr.ASR
}

func NewAudioParser(asrModel asr.ASR) *AudioParser {
	return &AudioParser{
		asrModel: asrModel,
	}
}

func (p *AudioParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	audioData, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取音频文件失败: %w", err)
	}

	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["size"] = len(audioData)
	metadata["type"] = "audio"

	if p.asrModel == nil {
		return fmt.Sprintf("[音频文件: %s, 大小: %d 字节]", filename, len(audioData)), metadata, nil
	}

	transcript, transErr := p.asrModel.Transcribe(ctx, audioData, filename)
	if transErr == nil && transcript != "" {
		metadata["transcript"] = transcript
		return transcript, metadata, nil
	}

	return fmt.Sprintf("[音频文件: %s, 大小: %d 字节]", filename, len(audioData)), metadata, nil
}

func (p *AudioParser) SupportedTypes() []string {
	return []string{"mp3", "wav", "flac", "aac", "ogg", "m4a", "wma"}
}
