package qqbridge

import (
	"testing"

	"Logos/config"
	"Logos/internal/service/messaging/types"
)

func TestAbsoluteMediaURL(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		raw    string
		want   string
		wantOK bool
	}{
		{
			name:   "relative path uses public base",
			base:   "https://chat.example.com/",
			raw:    "/api/v1/file/minio/qq-media/2026/11/04/a.jpg",
			want:   "https://chat.example.com/api/v1/file/minio/qq-media/2026/11/04/a.jpg",
			wantOK: true,
		},
		{
			name:   "absolute https url passes through",
			raw:    "https://cdn.example.com/a.jpg",
			want:   "https://cdn.example.com/a.jpg",
			wantOK: true,
		},
		{
			name:   "absolute http url passes through",
			raw:    "http://cdn.example.com/a.jpg",
			want:   "http://cdn.example.com/a.jpg",
			wantOK: true,
		},
		{
			name:   "relative without base is rejected",
			raw:    "/api/v1/file/minio/a.jpg",
			wantOK: false,
		},
		{
			name:   "non-http scheme rejected",
			raw:    "file:///etc/passwd",
			wantOK: false,
		},
		{
			name:   "empty rejected",
			raw:    "  ",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := NewZeroBotPlugin(nil, &config.Config{
				Gateway: config.GatewayCfg{PublicBaseURL: tc.base},
			})
			got, ok := plugin.absoluteMediaURL(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("url = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLogosEventToQQMessage_SkipsRelativeMediaWithoutBase(t *testing.T) {
	plugin := NewZeroBotPlugin(nil, &config.Config{})
	msg := plugin.LogosEventToQQMessage(&types.MessageEvent{
		MessageType: types.MessageTypeImage,
		Content:     "看这张图",
		MediaURL:    "/api/v1/file/minio/qq-media/a.jpg",
	})

	if len(msg) != 1 {
		t.Fatalf("缺少公网基址时应只发文本，got %d 段", len(msg))
	}
}

func TestLogosEventToQQMessage_UsesAbsoluteMediaURL(t *testing.T) {
	plugin := NewZeroBotPlugin(nil, &config.Config{
		Gateway: config.GatewayCfg{PublicBaseURL: "https://chat.example.com"},
	})
	msg := plugin.LogosEventToQQMessage(&types.MessageEvent{
		MessageType: types.MessageTypeImage,
		Content:     "看这张图",
		MediaURL:    "/api/v1/file/minio/qq-media/a.jpg",
	})

	if len(msg) != 2 {
		t.Fatalf("应包含文本 + 图片两段，got %d", len(msg))
	}
	if got := msg[1].Data["file"]; got != "https://chat.example.com/api/v1/file/minio/qq-media/a.jpg" {
		t.Fatalf("图片 URL = %v", got)
	}
}

func TestLogosEventToQQMessage_MediaOnlyMessageIsNotEmpty(t *testing.T) {
	plugin := NewZeroBotPlugin(nil, &config.Config{
		Gateway: config.GatewayCfg{PublicBaseURL: "https://chat.example.com"},
	})
	msg := plugin.LogosEventToQQMessage(&types.MessageEvent{
		MessageType: types.MessageTypeImage,
		MediaURL:    "/api/v1/file/minio/qq-media/a.jpg",
	})

	if len(msg) != 1 {
		t.Fatalf("纯图片消息应生成 1 段，got %d", len(msg))
	}
	if msg[0].Type != "image" {
		t.Fatalf("段类型 = %q, want image", msg[0].Type)
	}
}

func TestLogosEventToQQMessage_VoiceAndVideo(t *testing.T) {
	plugin := NewZeroBotPlugin(nil, &config.Config{
		Gateway: config.GatewayCfg{PublicBaseURL: "https://chat.example.com"},
	})
	for _, tc := range []struct {
		messageType types.MessageType
		wantType    string
	}{
		{types.MessageTypeVoice, "record"},
		{types.MessageTypeVideo, "video"},
	} {
		msg := plugin.LogosEventToQQMessage(&types.MessageEvent{
			MessageType: tc.messageType,
			MediaURL:    "/api/v1/file/minio/qq-media/a.bin",
		})
		if len(msg) != 1 || msg[0].Type != tc.wantType {
			t.Fatalf("type=%v 生成 %d 段，段类型 %q", tc.messageType, len(msg), msg[0].Type)
		}
	}
}
