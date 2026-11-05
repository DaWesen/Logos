package video

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestGetVideoInfoFailsWhenFFmpegUnavailable(t *testing.T) {
	extractor := &FFMpegExtractor{
		ffmpegPath: "ffmpeg-not-installed-for-test",
		tempDir:    t.TempDir(),
	}

	info, err := extractor.GetVideoInfo(context.Background(), []byte("not a video"))
	if err == nil {
		t.Fatalf("GetVideoInfo() = %v, want error when ffmpeg is unavailable", info)
	}
	if info != nil {
		t.Fatalf("GetVideoInfo() returned partial info %v together with error", info)
	}
}

func TestParseFFmpegDuration(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
		wantErr bool
	}{
		{
			name: "整秒时长",
			output: "Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'input.mp4':\n" +
				"  Duration: 00:01:23.45, start: 0.000000, bitrate: 1200 kb/s\n",
			want: 83.45,
		},
		{
			name:   "超过一小时",
			output: "  Duration: 02:15:30.50, start: 0.000000, bitrate: 800 kb/s\n",
			want:   2*3600 + 15*60 + 30.5,
		},
		{
			name:    "N/A 时长",
			output:  "  Duration: N/A, start: 0.000000, bitrate: N/A\n",
			wantErr: true,
		},
		{
			name:    "缺少 Duration 行",
			output:  "  Stream #0:0: Video: h264, yuv420p, 1920x1080\n",
			wantErr: true,
		},
		{
			name:    "小时不是数字",
			output:  "  Duration: abc:def:ghi, start: 0.0\n",
			wantErr: true,
		},
		{
			name:    "秒有多余小数点",
			output:  "  Duration: 00:00:1.2.3, start: 0.0\n",
			wantErr: true,
		},
		{
			name:    "分钟越界",
			output:  "  Duration: 00:99:00.00, start: 0.0\n",
			wantErr: true,
		},
		{
			name:    "秒越界",
			output:  "  Duration: 00:00:60.00, start: 0.0\n",
			wantErr: true,
		},
		{
			name:    "负数秒",
			output:  "  Duration: 00:00:-1.0, start: 0.0\n",
			wantErr: true,
		},
		{
			name:    "段数不足",
			output:  "  Duration: 00:83.45, start: 0.0\n",
			wantErr: true,
		},
		{
			name:    "空输出",
			output:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFFmpegDuration(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFFmpegDuration() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFFmpegDuration() unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-6 {
				t.Fatalf("parseFFmpegDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseFFmpegVideoSize(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantWidth  int
		wantHeight int
		wantErr    bool
	}{
		{
			name: "1080p 带 SAR DAR",
			output: "  Stream #0:0: Video: h264 (High), yuv420p, 1920x1080 [SAR 1:1 DAR 16:9], " +
				"25 fps, 25 tbr, 90k tbn\n",
			wantWidth:  1920,
			wantHeight: 1080,
		},
		{
			name: "竖屏带编码标识",
			output: "  Stream #0:0[0x1](und): Video: h264 (avc1 / 0x31637661), yuv420p(tv), " +
				"1080x1920, 1500 kb/s, 30 fps\n",
			wantWidth:  1080,
			wantHeight: 1920,
		},
		{
			name:       "行尾带逗号",
			output:     "  Stream #0:1: Video: mjpeg, yuvj420p, 640x480,\n",
			wantWidth:  640,
			wantHeight: 480,
		},
		{
			name:    "缺少 Video 行",
			output:  "  Stream #0:0: Audio: aac, 48000 Hz, stereo\n",
			wantErr: true,
		},
		{
			name:    "Video 行没有分辨率",
			output:  "  Stream #0:0: Video: h264, yuv420p\n",
			wantErr: true,
		},
		{
			name:    "分辨率是占位符",
			output:  "  Stream #0:0: Video: h264, yuv420p, 0x0\n",
			wantErr: true,
		},
		{
			name:    "空输出",
			output:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height, err := parseFFmpegVideoSize(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFFmpegVideoSize() = %dx%d, want error", width, height)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFFmpegVideoSize() unexpected error: %v", err)
			}
			if width != tt.wantWidth || height != tt.wantHeight {
				t.Fatalf("parseFFmpegVideoSize() = %dx%d, want %dx%d",
					width, height, tt.wantWidth, tt.wantHeight)
			}
		})
	}
}

func TestParseDimensionTokenRejectsNonResolutionFields(t *testing.T) {
	tests := []string{
		"(avc1 / 0x31637661)",
		"0x31637661",
		"[SAR 1:1 DAR 16:9]",
		"1920X1080",
		"1920x",
		"x1080",
		"1920x1080i",
		"99999x99999",
	}

	for _, field := range tests {
		if width, height, ok := parseDimensionToken(field); ok {
			t.Fatalf("parseDimensionToken(%q) = %dx%d, want not ok", field, width, height)
		}
	}
}

func TestParseFFmpegVideoSizeSkipsHexCodecTag(t *testing.T) {
	// 0x31637661 里也有 "x"，若按旧实现用 strings.Split(part, "x") 会先命中它，
	// 得到 width=0、height=31637661，这里锁定「必须跳过编码标识」。
	output := "  Stream #0:0: Video: h264 (avc1 / 0x31637661), yuv420p, 1280x720, 30 fps\n"

	width, height, err := parseFFmpegVideoSize(output)
	if err != nil {
		t.Fatalf("parseFFmpegVideoSize() unexpected error: %v", err)
	}
	if width != 1280 || height != 720 {
		t.Fatalf("parseFFmpegVideoSize() = %dx%d, want 1280x720", width, height)
	}
}

func TestParseFFmpegDurationIgnoresLaterDurationLines(t *testing.T) {
	// 有的容器会在流信息里再次出现 Duration，取第一条（容器级）即可。
	output := strings.Join([]string{
		"  Duration: 00:00:10.00, start: 0.000000, bitrate: 500 kb/s",
		"    Stream #0:0: Video: h264, yuv420p, 320x240",
		"  Duration: 00:00:99.00, start: 0.000000, bitrate: 500 kb/s",
	}, "\n")

	got, err := parseFFmpegDuration(output)
	if err != nil {
		t.Fatalf("parseFFmpegDuration() unexpected error: %v", err)
	}
	if got != 10 {
		t.Fatalf("parseFFmpegDuration() = %v, want 10", got)
	}
}
