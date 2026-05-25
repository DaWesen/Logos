package parser

import (
	"context"
	"fmt"
	"io"
	"strings"

	"Logos/internal/models/vlm"
	"Logos/pkg/logger"
)

const (
	vlmOCRPrompt = "提取这张图片中的所有文字内容，以纯Markdown格式输出。要求：\n" +
		"1. 忽略页眉页脚\n" +
		"2. 表格用Markdown表格语法\n" +
		"3. 公式用LaTeX格式（用$或$$包裹）\n" +
		"4. 按原始阅读顺序组织内容\n" +
		"5. 只输出提取的文本内容，不要添加任何HTML标签\n" +
		"如果图片中没有可识别的文字内容，回复：No text content."

	vlmCaptionPrompt = "请详细描述这张图片的内容，包括角色特征、服饰、场景、色彩、动作等细节信息"
)

type ImageParser struct {
	vlmModel vlm.VLM
}

func NewImageParser(vlmModel vlm.VLM) *ImageParser {
	return &ImageParser{
		vlmModel: vlmModel,
	}
}

func (p *ImageParser) SetVLMModel(model vlm.VLM) {
	p.vlmModel = model
}

func (p *ImageParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	imageData, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取图像文件失败: %w", err)
	}

	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["size"] = len(imageData)
	metadata["type"] = "image"

	if p.vlmModel == nil {
		return fmt.Sprintf("[图片文件: %s, 大小: %d 字节]", filename, len(imageData)), metadata, nil
	}

	var ocrText, caption string
	var hasOCR, hasCaption bool

	ocrResult, ocrErr := p.vlmModel.Predict(ctx, imageData, vlmOCRPrompt)
	if ocrErr != nil {
		logger.Warn("VLM OCR调用失败", logger.ErrorField(ocrErr))
	} else {
		ocrText = sanitizeOCRText(ocrResult)
		if ocrText != "" {
			hasOCR = true
			metadata["ocr"] = ocrText
		}
	}

	captionResult, capErr := p.vlmModel.Predict(ctx, imageData, vlmCaptionPrompt)
	if capErr != nil {
		logger.Warn("VLM Caption调用失败", logger.ErrorField(capErr))
	} else {
		caption = captionResult
		if caption != "" {
			hasCaption = true
			metadata["caption"] = caption
		}
	}

	var contentBuilder strings.Builder
	if hasCaption {
		contentBuilder.WriteString(fmt.Sprintf("【图片描述】：%s\n\n", caption))
	}
	if hasOCR {
		contentBuilder.WriteString(fmt.Sprintf("【图片中的文字】：%s", ocrText))
	}

	if !hasOCR && !hasCaption {
		return fmt.Sprintf("[图片文件: %s, 大小: %d 字节]", filename, len(imageData)), metadata, nil
	}

	return contentBuilder.String(), metadata, nil
}

func (p *ImageParser) SupportedTypes() []string {
	return []string{"jpg", "jpeg", "png", "gif", "webp", "bmp", "tiff", "svg"}
}

func sanitizeOCRText(text string) string {
	text = strings.TrimSpace(text)
	if text == "No text content" || text == "No text content." {
		return ""
	}
	return text
}
