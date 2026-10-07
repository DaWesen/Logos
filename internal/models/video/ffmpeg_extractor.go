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

func (e *FFMpegExtractor) getVideoDuration(ctx context.Context, videoPath string) (float64, error) {
	cmd := exec.CommandContext(ctx, e.ffmpegPath,
		"-i", videoPath,
		"-f", "null",
		"-")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}

	outputStr := string(output)
	durationStr := ""
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Duration:") {
			parts := strings.Split(line, "Duration:")
			if len(parts) > 1 {
				durationPart := strings.TrimSpace(parts[1])
				durationStr = strings.Split(durationPart, ",")[0]
				break
			}
		}
	}

	if durationStr == "" {
		return 0, fmt.Errorf("could not parse duration")
	}

	timeParts := strings.Split(durationStr, ":")
	if len(timeParts) != 3 {
		return 0, fmt.Errorf("invalid duration format: %s", durationStr)
	}

	hours, _ := strconv.Atoi(timeParts[0])
	minutes, _ := strconv.Atoi(timeParts[1])
	seconds, _ := strconv.ParseFloat(timeParts[2], 64)

	totalSeconds := float64(hours*3600) + float64(minutes*60) + seconds
	return totalSeconds, nil
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

	duration, err := e.getVideoDuration(ctx, tempVideoPath)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, e.ffmpegPath, "-i", tempVideoPath, "-f", "null", "-")
	output, _ := cmd.CombinedOutput()
	outputStr := string(output)

	width, height := 0, 0
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Video:") {
			parts := strings.Split(line, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.Contains(part, "x") && !strings.Contains(part, "SAR") {
					resParts := strings.Split(part, "x")
					if len(resParts) == 2 {
						width, _ = strconv.Atoi(strings.TrimSpace(resParts[0]))
						heightStr := strings.TrimSpace(resParts[1])
						heightParts := strings.Split(heightStr, " ")
						if len(heightParts) > 0 {
							height, _ = strconv.Atoi(heightParts[0])
						}
					}
					break
				}
			}
			break
		}
	}

	videoInfo := map[string]interface{}{
		"duration": duration,
		"width":    width,
		"height":   height,
	}

	return videoInfo, nil
}
