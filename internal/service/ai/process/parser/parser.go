package parser

import (
	"context"
	"io"

	"Logos/internal/models/asr"
	"Logos/internal/models/video"
	"Logos/internal/models/vlm"
)

type DocumentParser interface {
	Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error)
	SupportedTypes() []string
}

type ParserManager struct {
	parsers map[string]DocumentParser
}

func NewParserManager(vlmModel vlm.VLM, asrModel asr.ASR, videoExtractor video.Extractor) *ParserManager {
	pm := &ParserManager{
		parsers: make(map[string]DocumentParser),
	}
	pm.registerDefaultParsers(vlmModel, asrModel, videoExtractor)
	return pm
}

func (pm *ParserManager) registerDefaultParsers(vlmModel vlm.VLM, asrModel asr.ASR, videoExtractor video.Extractor) {
	parsers := []DocumentParser{
		NewTextParser(),
		NewImageParser(vlmModel),
		NewAudioParser(asrModel),
		NewVideoParser(vlmModel, asrModel, videoExtractor),
		NewDocParser(),
		NewGenericParser(),
	}

	for _, parser := range parsers {
		for _, t := range parser.SupportedTypes() {
			pm.parsers[t] = parser
		}
	}
}

func (pm *ParserManager) RegisterParser(fileType string, parser DocumentParser) {
	pm.parsers[fileType] = parser
}

func (pm *ParserManager) GetParser(fileType string) DocumentParser {
	return pm.parsers[fileType]
}

func (pm *ParserManager) HasParser(fileType string) bool {
	_, ok := pm.parsers[fileType]
	return ok
}
