package video

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"Logos/pkg/logger"
)

type FFMpegExtractor struct {
	ffmpegPath string
	tempDir    string
}

func NewFFMpegExtractor(config *Config) (Extractor, error) {
	ffmpegPath := "ffmpeg"
	tempDir := os.TempDir()

	if config != nil {
		if config.FFMpegPath != "" {
			ffmpegPath = config.FFMpegPath
		}
		if config.TempDir != "" {
			tempDir = config.TempDir
		}
	}

	extractor := &FFMpegExtractor{ffmpegPath: ffmpegPath, tempDir: tempDir}
	if err := extractor.checkFFmpeg(); err != nil {
		return nil, err
	}
	return extractor, nil
}

func (e *FFMpegExtractor) checkFFmpeg() error {
	cmd := exec.Command(e.ffmpegPath, "-version")
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg not found or not executable: %w", err)
	}
	return nil
}

func (e *FFMpegExtractor) ExtractFrames(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]*Frame, error) {
	if options == nil {
		options = DefaultExtractOptions()
	}

	tempVideoPath, err := e.writeTempVideo(videoBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tempVideoPath)

	tempFrameDir, err := os.MkdirTemp(e.tempDir, "video-frames-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp frame directory: %w", err)
	}
	defer os.RemoveAll(tempFrameDir)

	switch options.Mode {
	case ExtractModeTimestamps:
		err = e.extractFramesByTimestamps(ctx, tempVideoPath, tempFrameDir, options)
	case ExtractModeScene:
		err = e.extractFramesByScene(ctx, tempVideoPath, tempFrameDir, options)
	case ExtractModeKeyFrame:
		err = e.extractFramesByKeyFrame(ctx, tempVideoPath, tempFrameDir, options)
	default:
		err = e.extractFramesByInterval(ctx, tempVideoPath, tempFrameDir, options)
	}

	if err != nil {
		return nil, err
	}

	return e.readExtractedFrames(tempFrameDir, options)
}

func (e *FFMpegExtractor) ExtractFramesToReaders(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]io.Reader, error) {
	frames, err := e.ExtractFrames(ctx, videoBytes, options)
	if err != nil {
		return nil, err
	}

	readers := make([]io.Reader, 0, len(frames))
	for _, frame := range frames {
		readers = append(readers, bytes.NewReader(frame.ImageData))
	}

	return readers, nil
}

func (e *FFMpegExtractor) writeTempVideo(videoBytes []byte) (string, error) {
	tempFile, err := os.CreateTemp(e.tempDir, "video-input-*.mp4")
	if err != nil {
		return "", fmt.Errorf("failed to create temp video file: %w", err)
	}
	defer tempFile.Close()

	if _, err := tempFile.Write(videoBytes); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write video bytes: %w", err)
	}

	return tempFile.Name(), nil
}

// probeVideo 只让 ffmpeg 读取媒体头信息，不产出任何文件。
// ffmpeg 在只给 -i 而没有输出目标时会以非零码退出，但 stderr 里的
// 媒体信息是完整的，所以这里只把「拿不到任何输出」当作失败。
func (e *FFMpegExtractor) probeVideo(ctx context.Context, videoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, e.ffmpegPath, "-hide_banner", "-i", videoPath)
	output, err := cmd.CombinedOutput()

	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("ffmpeg probe canceled: %w", ctxErr)
	}
	if len(output) == 0 {
		if err != nil {
			return "", fmt.Errorf("ffmpeg probe failed with no output: %w", err)
		}
		return "", fmt.Errorf("ffmpeg probe produced no output")
	}

	return string(output), nil
}

func parseFFmpegDuration(output string) (float64, error) {
	durationStr := ""
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, "Duration:")
		if idx < 0 {
			continue
		}
		durationPart := strings.TrimSpace(line[idx+len("Duration:"):])
		durationStr = strings.TrimSpace(strings.Split(durationPart, ",")[0])
		break
	}

	if durationStr == "" {
		return 0, fmt.Errorf("could not find duration in ffmpeg output")
	}
	if durationStr == "N/A" {
		return 0, fmt.Errorf("ffmpeg reported duration as N/A")
	}

	timeParts := strings.Split(durationStr, ":")
	if len(timeParts) != 3 {
		return 0, fmt.Errorf("invalid duration format: %q", durationStr)
	}

	hours, err := strconv.Atoi(strings.TrimSpace(timeParts[0]))
	if err != nil {
		return 0, fmt.Errorf("invalid duration hours %q: %w", timeParts[0], err)
	}
	if hours < 0 {
		return 0, fmt.Errorf("invalid duration hours %q: must not be negative", timeParts[0])
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(timeParts[1]))
	if err != nil || minutes < 0 || minutes > 59 {
		return 0, fmt.Errorf("invalid duration minutes %q", timeParts[1])
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(timeParts[2]), 64)
	if err != nil || seconds < 0 || seconds >= 60 {
		return 0, fmt.Errorf("invalid duration seconds %q", timeParts[2])
	}

	return float64(hours*3600+minutes*60) + seconds, nil
}

func parseFFmpegVideoSize(output string) (int, int, error) {
	var videoLine string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Video:") {
			videoLine = line
			break
		}
	}

	if videoLine == "" {
		return 0, 0, fmt.Errorf("could not find a video stream in ffmpeg output")
	}

	if width, height, ok := parseVideoSizeLine(videoLine); ok {
		return width, height, nil
	}

	return 0, 0, fmt.Errorf("could not parse video resolution from stream line: %q", strings.TrimSpace(videoLine))
}

// parseVideoSizeLine 只在「以数字开头的独立字段」里找 WxH。
// 形如 (avc1 / 0x31637661) 的十六进制编码标识虽然也含 x，但不是分辨率。
func parseVideoSizeLine(line string) (int, int, bool) {
	idx := strings.Index(line, "Video:")
	if idx < 0 {
		return 0, 0, false
	}

	for _, field := range strings.Split(line[idx+len("Video:"):], ",") {
		field = strings.TrimSpace(field)
		width, height, ok := parseDimensionToken(field)
		if ok {
			return width, height, true
		}
	}

	return 0, 0, false
}

func parseDimensionToken(field string) (int, int, bool) {
	xIdx := 0
	for xIdx < len(field) && field[xIdx] >= '0' && field[xIdx] <= '9' {
		xIdx++
	}
	if xIdx == 0 || xIdx >= len(field) || field[xIdx] != 'x' {
		return 0, 0, false
	}

	endIdx := xIdx + 1
	for endIdx < len(field) && field[endIdx] >= '0' && field[endIdx] <= '9' {
		endIdx++
	}
	if endIdx == xIdx+1 {
		return 0, 0, false
	}
	if endIdx < len(field) && field[endIdx] != ' ' && field[endIdx] != '[' {
		return 0, 0, false
	}

	width, err := strconv.Atoi(field[:xIdx])
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(field[xIdx+1 : endIdx])
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return 0, 0, false
	}

	return width, height, true
}

func (e *FFMpegExtractor) buildScaleFilter(options *ExtractOptions) string {
	if options.Width <= 0 && options.Height <= 0 {
		return ""
	}
	scaleFilter := "scale="
	if options.Width > 0 {
		scaleFilter += strconv.Itoa(options.Width)
	} else {
		scaleFilter += "-1"
	}
	scaleFilter += ":"
	if options.Height > 0 {
		scaleFilter += strconv.Itoa(options.Height)
	} else {
		scaleFilter += "-1"
	}
	return scaleFilter
}

func (e *FFMpegExtractor) appendOutputArgs(args []string, options *ExtractOptions, outputDir string) []string {
	quality := options.Quality
	if quality < 1 {
		quality = 1
	} else if quality > 100 {
		quality = 100
	}
	if options.Format == "jpeg" {
		args = append(args, "-q:v", strconv.Itoa(quality))
	}

	outputPattern := filepath.Join(outputDir, "frame-%05d."+options.Format)
	args = append(args, outputPattern)
	return args
}

func (e *FFMpegExtractor) runFFmpeg(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, e.ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg extraction failed: %w, stderr: %s", err, stderr.String())
	}
	return nil
}

func (e *FFMpegExtractor) extractFramesByInterval(ctx context.Context, videoPath, outputDir string, options *ExtractOptions) error {
	filters := []string{fmt.Sprintf("fps=1/%f", options.FrameInterval)}
	if scale := e.buildScaleFilter(options); scale != "" {
		filters = append(filters, scale)
	}

	args := []string{
		"-i", videoPath,
		"-vf", strings.Join(filters, ","),
	}
	args = e.appendOutputArgs(args, options, outputDir)

	logger.Info("按间隔抽帧", logger.StringField("interval", fmt.Sprintf("%.2fs", options.FrameInterval)))
	return e.runFFmpeg(ctx, args)
}

func (e *FFMpegExtractor) extractFramesByKeyFrame(ctx context.Context, videoPath, outputDir string, options *ExtractOptions) error {
	var filters []string
	if scale := e.buildScaleFilter(options); scale != "" {
		filters = append(filters, scale)
	}

	args := []string{
		"-i", videoPath,
		"-vf", "select='eq(pict_type\\,I)'",
		"-vsync", "vfr",
	}
	if len(filters) > 0 {
		args[3] = "select='eq(pict_type\\,I)'," + strings.Join(filters, ",")
	}
	args = e.appendOutputArgs(args, options, outputDir)

	logger.Info("按关键帧抽帧")
	return e.runFFmpeg(ctx, args)
}

func (e *FFMpegExtractor) extractFramesByScene(ctx context.Context, videoPath, outputDir string, options *ExtractOptions) error {
	threshold := options.SceneThreshold
	if threshold <= 0 {
		threshold = 0.3
	}

	selectFilter := fmt.Sprintf("select='gt(scene\\,%.4f)'", threshold)
	filters := []string{selectFilter}
	if scale := e.buildScaleFilter(options); scale != "" {
		filters = append(filters, scale)
	}

	args := []string{
		"-i", videoPath,
		"-vf", strings.Join(filters, ","),
		"-vsync", "vfr",
	}
	args = e.appendOutputArgs(args, options, outputDir)

	logger.Info("按场景变化抽帧", logger.StringField("threshold", fmt.Sprintf("%.4f", threshold)))
	return e.runFFmpeg(ctx, args)
}

func (e *FFMpegExtractor) extractFramesByTimestamps(ctx context.Context, videoPath, outputDir string, options *ExtractOptions) error {
	if len(options.Timestamps) == 0 {
		return fmt.Errorf("timestamps mode requires at least one timestamp")
	}

	for i, ts := range options.Timestamps {
		var filters []string
		if scale := e.buildScaleFilter(options); scale != "" {
			filters = append(filters, scale)
		}

		args := []string{
			"-ss", fmt.Sprintf("%.6f", ts),
			"-i", videoPath,
			"-frames:v", "1",
		}
		if len(filters) > 0 {
			args = append(args, "-vf", strings.Join(filters, ","))
		}

		quality := options.Quality
		if quality < 1 {
			quality = 1
		} else if quality > 100 {
			quality = 100
		}
		if options.Format == "jpeg" {
			args = append(args, "-q:v", strconv.Itoa(quality))
		}

		outputFile := filepath.Join(outputDir, fmt.Sprintf("frame-%05d.%s", i, options.Format))
		args = append(args, outputFile)

		if err := e.runFFmpeg(ctx, args); err != nil {
			logger.Warn("指定时间戳抽帧失败",
				logger.IntField("index", i),
				logger.StringField("timestamp", fmt.Sprintf("%.2fs", ts)),
				logger.ErrorField(err))
			continue
		}
	}

	logger.Info("按时间戳抽帧", logger.IntField("count", len(options.Timestamps)))
	return nil
}

func (e *FFMpegExtractor) readExtractedFrames(frameDir string, options *ExtractOptions) ([]*Frame, error) {
	files, err := filepath.Glob(filepath.Join(frameDir, "frame-*."+options.Format))
	if err != nil {
		return nil, fmt.Errorf("failed to list frame files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no frames were extracted")
	}

	frames := make([]*Frame, 0, len(files))
	for i, filePath := range files {
		imgData, err := os.ReadFile(filePath)
		if err != nil {
			logger.Warn("Failed to read frame", logger.IntField("index", i), logger.ErrorField(err))
			continue
		}

		frame := &Frame{
			Index:     i,
			Timestamp: float64(i) * options.FrameInterval,
			ImageData: imgData,
			Format:    options.Format,
			Width:     options.Width,
			Height:    options.Height,
		}

		frames = append(frames, frame)

		if options.MaxFrames > 0 && len(frames) >= options.MaxFrames {
			break
		}
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames could be read")
	}

	logger.Info("Successfully read frames", logger.IntField("count", len(frames)))
	return frames, nil
}

func (e *FFMpegExtractor) GetVideoInfo(ctx context.Context, videoBytes []byte) (map[string]interface{}, error) {
	tempVideoPath, err := e.writeTempVideo(videoBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tempVideoPath)

	output, err := e.probeVideo(ctx, tempVideoPath)
	if err != nil {
		return nil, err
	}

	duration, err := parseFFmpegDuration(output)
	if err != nil {
		return nil, fmt.Errorf("failed to read video duration: %w", err)
	}
	width, height, err := parseFFmpegVideoSize(output)
	if err != nil {
		return nil, fmt.Errorf("failed to read video resolution: %w", err)
	}

	videoInfo := map[string]interface{}{
		"duration": duration,
		"width":    width,
		"height":   height,
	}

	return videoInfo, nil
}
