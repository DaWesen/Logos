package qqbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/uploadguard"
)

func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				_ = os.Chdir(dir)
				_ = os.Setenv("LOGOS_CONFIG_PATH", filepath.Join(dir, "config", "testdata", "config.yaml"))
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	os.Exit(m.Run())
}

var testJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 512)...)

type fakeMediaStore struct {
	bucket      string
	object      string
	size        int64
	contentType string
	data        []byte
	err         error
	ctxErr      error
}

func (s *fakeMediaStore) UploadFileContext(
	ctx context.Context,
	bucketName, objectName string,
	reader io.Reader,
	size int64,
	contentType string,
) error {
	if err := ctx.Err(); err != nil {
		s.ctxErr = err
		return err
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.bucket = bucketName
	s.object = objectName
	s.size = size
	s.contentType = contentType
	s.data = data
	return s.err
}

func newTestRelay(t *testing.T, store mediaUploader, client *http.Client) *MediaRelay {
	t.Helper()
	relay := NewMediaRelay(&config.Config{})
	relay.tempDir = t.TempDir()
	relay.client = client
	relay.validateURL = func(string) error { return nil }
	relay.store = store
	return relay
}

func TestMediaRelay_UploadToMinIOUsesOpaqueKey(t *testing.T) {
	relay := newTestRelay(t, &fakeMediaStore{}, http.DefaultClient)
	relay.now = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

	localPath := filepath.Join(relay.tempDir, "avatar.html")
	if err := os.WriteFile(localPath, testJPEG, 0600); err != nil {
		t.Fatal(err)
	}

	store := relay.store.(*fakeMediaStore)
	url, meta, err := relay.UploadToMinIO(context.Background(), localPath)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if len(meta) == 0 {
		t.Fatal("媒体元数据不应为空")
	}
	var metaObj map[string]any
	if err := json.Unmarshal(meta, &metaObj); err != nil {
		t.Fatalf("媒体元数据不是合法 JSON: %v", err)
	}
	if metaObj["object_key"] != store.object {
		t.Fatalf("元数据对象键 = %v, want %s", metaObj["object_key"], store.object)
	}
	if metaObj["mime_type"] != "image/jpeg" {
		t.Fatalf("元数据 MIME = %v, want image/jpeg", metaObj["mime_type"])
	}
	if !strings.HasPrefix(url, "/api/v1/file/minio/qq-media/2026/11/04/") {
		t.Fatalf("对象 URL 不符合预期: %s", url)
	}
	if !strings.HasSuffix(store.object, ".jpg") {
		t.Fatalf("应以内容嗅探结果为准生成 .jpg 对象键，got %s", store.object)
	}
	if strings.Contains(store.object, "avatar") || strings.Contains(store.object, "..") {
		t.Fatalf("对象键不能包含原始文件名或路径: %s", store.object)
	}
	if store.contentType != "image/jpeg" {
		t.Fatalf("content-type = %s, want image/jpeg", store.contentType)
	}
	if store.bucket != defaultMediaBucket {
		t.Fatalf("未配置桶名时应使用 %s，got %s", defaultMediaBucket, store.bucket)
	}
	if store.size != int64(len(testJPEG)) || len(store.data) != len(testJPEG) {
		t.Fatalf("上传内容不完整: size=%d read=%d", store.size, len(store.data))
	}
}

func TestMediaRelay_DetectContentTypeRejectsHTML(t *testing.T) {
	got := detectMediaContentType([]byte("<html><script>alert(1)</script></html>"), ".jpg")
	if got != "application/octet-stream" {
		t.Fatalf("HTML 不能伪装成图片，got %s", got)
	}
}

func TestMediaRelay_DownloadRejectsOversizeContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	relay := newTestRelay(t, &fakeMediaStore{}, srv.Client())
	relay.maxBytes = 16
	_, err := relay.DownloadFromQQ(context.Background(), srv.URL, "x.jpg")
	if !errors.Is(err, uploadguard.ErrTooLarge) {
		t.Fatalf("期望 ErrTooLarge，got %v", err)
	}
}

func TestMediaRelay_DownloadUsesRandomLocalName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(testJPEG)
	}))
	defer srv.Close()

	relay := newTestRelay(t, &fakeMediaStore{}, srv.Client())
	localPath, err := relay.DownloadFromQQ(context.Background(), srv.URL, "../../evil.jpg")
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	defer relay.Cleanup(localPath)

	if filepath.Dir(localPath) != relay.tempDir {
		t.Fatalf("文件逃逸出临时目录: %s", localPath)
	}
	if strings.Contains(filepath.Base(localPath), "evil") {
		t.Fatalf("本地文件名不能来自上游: %s", localPath)
	}
	if filepath.Ext(localPath) != ".jpg" {
		t.Fatalf("扩展名 = %s, want .jpg", filepath.Ext(localPath))
	}
}

func TestMediaRelay_RelayInboundMediaSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(testJPEG)
	}))
	defer srv.Close()

	store := &fakeMediaStore{}
	relay := newTestRelay(t, store, srv.Client())
	event := &types.MessageEvent{
		MessageType: types.MessageTypeImage,
		Content:     "[图片]",
		Metadata:    map[string]string{"source": "qq"},
	}
	seg := OneBotSegment{Type: "image"}
	seg.Data.URL = srv.URL
	seg.Data.File = "photo.jpg"
	msg := &InboundMessage{Message: []OneBotSegment{seg}}

	if err := relay.RelayInboundMedia(context.Background(), event, msg); err != nil {
		t.Fatalf("媒体中转失败: %v", err)
	}
	if !strings.HasPrefix(event.MediaURL, "/api/v1/file/minio/qq-media/") {
		t.Fatalf("MediaURL = %q", event.MediaURL)
	}
	if event.Metadata["qq_media_relayed"] != "true" {
		t.Fatal("缺少 qq_media_relayed 标记")
	}
	if len(event.MediaMeta) == 0 {
		t.Fatal("媒体元数据应随消息落库")
	}
	if store.object == "" {
		t.Fatal("媒体没有上传到对象存储")
	}
}

func TestMediaRelay_UploadHonorsCanceledContext(t *testing.T) {
	relay := newTestRelay(t, &fakeMediaStore{}, http.DefaultClient)
	localPath := filepath.Join(relay.tempDir, "photo.jpg")
	if err := os.WriteFile(localPath, testJPEG, 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := relay.UploadToMinIO(ctx, localPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("上传应随 ctx 取消，got %v", err)
	}
	if relay.store != nil && relay.store.(*fakeMediaStore).object != "" {
		t.Fatal("ctx 已取消时不应真的发起上传")
	}
}

func TestMediaRelay_SweepRemovesStaleFilesOnly(t *testing.T) {
	relay := newTestRelay(t, &fakeMediaStore{}, http.DefaultClient)
	relay.now = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

	stale := filepath.Join(relay.tempDir, "stale.jpg")
	fresh := filepath.Join(relay.tempDir, "fresh.jpg")
	for path, modTime := range map[string]time.Time{
		stale: relay.now().Add(-2 * time.Hour),
		fresh: relay.now().Add(-10 * time.Minute),
	} {
		if err := os.WriteFile(path, testJPEG, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := relay.SweepTempDir(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("过期临时文件应被删除")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("正在使用的新文件不能被误删")
	}
}

func TestMediaRelay_SweepStopsOnCanceledContext(t *testing.T) {
	relay := newTestRelay(t, &fakeMediaStore{}, http.DefaultClient)
	relay.now = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

	path := filepath.Join(relay.tempDir, "stale.jpg")
	if err := os.WriteFile(path, testJPEG, 0600); err != nil {
		t.Fatal(err)
	}
	old := relay.now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	removed, err := relay.SweepTempDir(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 ctx 取消，got %v", err)
	}
	if removed != 0 {
		t.Fatalf("取消后不应继续删除，removed=%d", removed)
	}
}

func TestMediaRelay_RelayWaitsForSlotAndHonorsContext(t *testing.T) {
	relay := newTestRelay(t, &fakeMediaStore{}, http.DefaultClient)
	relay.sem = make(chan struct{}, 1)
	relay.sem <- struct{}{} // 占满唯一槽位
	defer func() { <-relay.sem }()

	event := &types.MessageEvent{MessageType: types.MessageTypeImage, Content: "[图片]"}
	seg := OneBotSegment{Type: "image"}
	seg.Data.URL = "https://example.com/a.jpg"
	msg := &InboundMessage{Message: []OneBotSegment{seg}}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := relay.RelayInboundMedia(ctx, event, msg)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("槽位耗尽时应随 ctx 超时，got %v", err)
	}
	if event.MediaURL != "" {
		t.Fatalf("未拿到槽位不应写 MediaURL: %q", event.MediaURL)
	}
}

func TestMediaRelay_RelayReleasesSlotOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(testJPEG)
	}))
	defer srv.Close()

	relay := newTestRelay(t, &fakeMediaStore{}, srv.Client())
	relay.sem = make(chan struct{}, 1)

	event := &types.MessageEvent{MessageType: types.MessageTypeImage, Content: "[图片]"}
	seg := OneBotSegment{Type: "image"}
	seg.Data.URL = srv.URL
	seg.Data.File = "photo.jpg"
	msg := &InboundMessage{Message: []OneBotSegment{seg}}

	if err := relay.RelayInboundMedia(context.Background(), event, msg); err != nil {
		t.Fatalf("媒体中转失败: %v", err)
	}
	if len(relay.sem) != 0 {
		t.Fatalf("成功后槽位未释放: %d", len(relay.sem))
	}
}

func TestMediaRelay_RelayInboundMediaFailureKeepsPlaceholder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer srv.Close()

	relay := newTestRelay(t, &fakeMediaStore{}, srv.Client())
	event := &types.MessageEvent{
		MessageType: types.MessageTypeImage,
		Content:     "[图片]",
	}
	seg := OneBotSegment{Type: "image"}
	seg.Data.URL = srv.URL
	msg := &InboundMessage{Message: []OneBotSegment{seg}}

	if err := relay.RelayInboundMedia(context.Background(), event, msg); err == nil {
		t.Fatal("期望上游失败")
	}
	if event.MediaURL != "" {
		t.Fatalf("失败时不应写入半成品 MediaURL: %q", event.MediaURL)
	}
	if event.Content != "[图片]" {
		t.Fatalf("文本占位不应被改写: %q", event.Content)
	}
}
