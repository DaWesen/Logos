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
	parsers        map[string]DocumentParser
	videoExtractor video.Extractor
	videoOptions   *video.ExtractOptions
}

func NewParserManager(vlmModel vlm.VLM, asrModel asr.ASR, videoExtractor video.Extractor) *ParserManager {
	pm := &ParserManager{
		parsers:        make(map[string]DocumentParser),
		videoExtractor: videoExtractor,
	}
	pm.registerDefaultParsers(vlmModel, asrModel, videoExtractor)
	return pm
}

func NewParserManagerWithOptions(vlmModel vlm.VLM, asrModel asr.ASR, videoExtractor video.Extractor, videoOptions *video.ExtractOptions) *ParserManager {
	pm := &ParserManager{
		parsers:        make(map[string]DocumentParser),
		videoExtractor: videoExtractor,
		videoOptions:   videoOptions,
	}
	pm.registerDefaultParsers(vlmModel, asrModel, videoExtractor)
	return pm
}

func (pm *ParserManager) registerDefaultParsers(vlmModel vlm.VLM, asrModel asr.ASR, videoExtractor video.Extractor) {
	parsers := []DocumentParser{
		NewCrawlerParser(),
		NewTextParser(),
		NewImageParser(vlmModel),
		NewAudioParser(asrModel),
		NewVideoParserWithOptions(vlmModel, asrModel, videoExtractor, pm.videoOptions),
		NewDocParser(),
		NewGenericParser(),
	}

	for _, parser := range parsers {
		for _, t := range parser.SupportedTypes() {
			pm.parsers[t] = parser
		}
	}
}

func (pm *ParserManager) SetVideoOptions(options *video.ExtractOptions) {
	pm.videoOptions = options
	pm.parsers["mp4"] = NewVideoParserWithOptions(nil, nil, pm.videoExtractor, options)
	for _, t := range pm.parsers["mp4"].SupportedTypes() {
		pm.parsers[t] = pm.parsers["mp4"]
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

func (pm *ParserManager) SetVLMModel(model vlm.VLM) {
	for _, parser := range pm.parsers {
		if imgParser, ok := parser.(*ImageParser); ok {
			imgParser.SetVLMModel(model)
		}
		if vidParser, ok := parser.(*VideoParser); ok {
			vidParser.SetVLMModel(model)
		}
	}
}
