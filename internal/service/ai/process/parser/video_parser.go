package parser

import (
	"context"
	"fmt"
	"io"
	"strings"

	"Logos/internal/models/asr"
	"Logos/internal/models/vlm"
	"Logos/internal/models/video"
)

const (
	vlmFramePrompt = "分析这个视频帧，用中文提供详细的描述。要求：\n" +
		"1. 描述可见的物体、人物以及他们的动作\n" +
		"2. 描述场景和环境\n" +
		"3. 提取并描述屏幕上的文字\n" +
		"4. 记录任何值得注意的事件或变化\n" +
		"5. 简洁但全面"

	vlmSummaryPrompt = "将以下视频帧描述整合成一个连贯的视频摘要，用中文输出。要求：\n" +
		"1. 描述是按时间戳顺序排列的\n" +
		"2. 关注主要情节和关键事件\n" +
		"3. 保持视频的时间流程\n" +
		"4. 简洁但全面"
)

type VideoParser struct {
	vlmModel   vlm.VLM
	asrModel   asr.ASR
	extractor  video.Extractor
}

func NewVideoParser(vlmModel vlm.VLM, asrModel asr.ASR, extractor video.Extractor) *VideoParser {
	return &VideoParser{
		vlmModel:  vlmModel,
		asrModel:  asrModel,
		extractor: extractor,
	}
}

func (p *VideoParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	videoData, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取视频文件失败: %w", err)
	}

	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["size"] = len(videoData)
	metadata["type"] = "video"

	if p.extractor == nil && p.vlmModel == nil && p.asrModel == nil {
		return fmt.Sprintf("[视频文件: %s, 大小: %d 字节]", filename, len(videoData)), metadata, nil
	}

	var videoInfo map[string]interface{}
	if p.extractor != nil {
		videoInfo, _ = p.extractor.GetVideoInfo(ctx, videoData)
		if videoInfo != nil {
			metadata["video_info"] = videoInfo
		}
	}

	var frameDescriptions []string
	if p.extractor != nil && p.vlmModel != nil {
		options := video.DefaultExtractOptions()
		options.MaxFrames = 5
		frames, extractErr := p.extractor.ExtractFrames(ctx, videoData, options)
		if extractErr == nil && len(frames) > 0 {
			for _, frame := range frames {
				desc, descErr := p.vlmModel.Predict(ctx, frame.ImageData, vlmFramePrompt)
				if descErr == nil {
					frameDesc := fmt.Sprintf("[%.2fs] %s", frame.Timestamp, desc)
					frameDescriptions = append(frameDescriptions, frameDesc)
				}
			}
			if len(frameDescriptions) > 0 {
				metadata["frame_descriptions"] = frameDescriptions
				metadata["frame_count"] = len(frameDescriptions)
			}
		}
	}

	var asrText string
	if p.asrModel != nil {
		asrResult, asrErr := p.asrModel.Transcribe(ctx, videoData, filename)
		if asrErr == nil && asrResult != "" {
			asrText = asrResult
			metadata["asr_text"] = asrText
		}
	}

	var videoSummary string
	if len(frameDescriptions) > 0 && p.vlmModel != nil {
		combinedInput := strings.Join(frameDescriptions, "\n")
		summary, sumErr := p.vlmModel.Predict(ctx, []byte(combinedInput), vlmSummaryPrompt)
		if sumErr == nil {
			videoSummary = summary
		} else {
			videoSummary = combinedInput
		}
	} else if len(frameDescriptions) > 0 {
		videoSummary = strings.Join(frameDescriptions, "\n")
	}

	var contentBuilder strings.Builder
	if videoSummary != "" {
		contentBuilder.WriteString(fmt.Sprintf("【视频摘要】：%s\n\n", videoSummary))
	}
	if asrText != "" {
		contentBuilder.WriteString(fmt.Sprintf("【视频中的音频】：%s\n\n", asrText))
	}
	if len(frameDescriptions) > 0 {
		contentBuilder.WriteString("【视频帧详情】：\n")
		for _, desc := range frameDescriptions {
			contentBuilder.WriteString(fmt.Sprintf("- %s\n", desc))
		}
	}

	if contentBuilder.Len() == 0 {
		return fmt.Sprintf("[视频文件: %s, 大小: %d 字节]", filename, len(videoData)), metadata, nil
	}

	return contentBuilder.String(), metadata, nil
}

func (p *VideoParser) SupportedTypes() []string {
	return []string{"mp4", "webm", "avi", "mov", "mkv", "flv", "wmv", "m4v"}
}
