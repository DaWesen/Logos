package qqbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/safehttp"
	"Logos/pkg/storage"
	"Logos/pkg/uploadguard"

	"github.com/google/uuid"
)

const (
	// maxQQMediaBytes QQ 入站媒体单文件上限的兜底值。
	// 实际值来自配置（qqbridge.media_max_bytes_mb），默认 100MB：
	// 比聊天上传的 500MB 更保守——Bridge 是常驻进程，不能让异常大文件
	// 长期占住临时目录和上传连接。
	maxQQMediaBytes int64 = 100 << 20
	// qqMediaObjectPrefix 与聊天媒体分开，便于按前缀配置生命周期。
	qqMediaObjectPrefix = "qq-media"
	// maxConcurrentMediaRelays 是媒体并发的默认值；实际值来自配置。
	// 不设上限时一次图片刷屏就能把磁盘/带宽打满。
	maxConcurrentMediaRelays = 8
	// defaultMediaBucket 与网关文件代理使用的默认桶保持一致。
	defaultMediaBucket = "logos-documents"
)

// mediaUploader 只依赖上传能力，测试可以注入内存实现，不需要起 MinIO。
type mediaUploader interface {
	UploadFileContext(ctx context.Context, bucketName, objectName string, reader io.Reader, size int64, contentType string) error
}

// MediaRelay QQ 媒体文件中转器。
type MediaRelay struct {
	cfg     *config.Config
	tempDir string
	client  *http.Client
	// validateURL 默认走 safehttp；测试注入 no-op，避免 httptest 的回环地址被拒绝。
	validateURL func(string) error

	storeMu sync.Mutex
	store   mediaUploader
	sem     chan struct{}

	maxBytes int64
	now      func() time.Time
}

// NewMediaRelay 创建媒体中转器。
func NewMediaRelay(cfg *config.Config) *MediaRelay {
	if cfg == nil {
		cfg = &config.Config{}
	}
	tempDir := cfg.QQBridge.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "qqbridge")
	}

	return &MediaRelay{
		cfg:     cfg,
		tempDir: tempDir,
		client:  safehttp.NewClient(60*time.Second, 3),
		validateURL: func(raw string) error {
			_, err := safehttp.ValidateURL(raw)
			return err
		},
		maxBytes: mediaMaxBytes(cfg),
		now:      time.Now,
		sem:      make(chan struct{}, mediaConcurrency(cfg)),
	}
}

// DownloadFromQQ 从 QQ CDN 下载媒体到临时目录。
//
// 这里不直接信任上游给的 URL 和文件名：
//   - URL 走 safehttp 的协议/私网/重定向校验，避免 Bridge 成为 SSRF 跳板；
//   - 文件名只取安全扩展名，本地文件用 UUID 命名，避免路径穿越和覆盖；
//   - 读取阶段用 LimitReader + 超限检查，不能只信 Content-Length。
func (m *MediaRelay) DownloadFromQQ(ctx context.Context, rawURL, filename string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("QQ媒体 URL 为空")
	}
	if m.validateURL != nil {
		if err := m.validateURL(rawURL); err != nil {
			return "", fmt.Errorf("QQ媒体 URL 不安全: %w", err)
		}
	}
	if err := os.MkdirAll(m.tempDir, 0755); err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		obs.RecordQQMediaRelay(ctx, "download", relayResult(ctx, err), 0)
		return "", fmt.Errorf("下载文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		obs.RecordQQMediaRelay(ctx, "download", "failed", 0)
		return "", fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}

	maxBytes := m.maxBytes
	if maxBytes <= 0 {
		maxBytes = maxQQMediaBytes
	}
	if resp.ContentLength > maxBytes {
		obs.RecordQQMediaRelay(ctx, "download", "too_large", 0)
		return "", fmt.Errorf("%w: QQ媒体声明大小 %d 超过 %d", uploadguard.ErrTooLarge, resp.ContentLength, maxBytes)
	}

	localPath := filepath.Join(m.tempDir, uuid.NewString()+uploadguard.SanitizeExt(filename))
	file, err := os.OpenFile(localPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("创建本地文件失败: %w", err)
	}

	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(localPath)
		}
	}()

	n, copyErr := uploadguard.Copy(file, resp.Body, maxBytes)
	closeErr := file.Close()
	if copyErr != nil {
		obs.RecordQQMediaRelay(ctx, "download", relayResult(ctx, copyErr), n)
		return "", fmt.Errorf("写入文件失败: %w", copyErr)
	}
	if closeErr != nil {
		obs.RecordQQMediaRelay(ctx, "download", "failed", n)
		return "", fmt.Errorf("关闭本地文件失败: %w", closeErr)
	}
	if n == 0 {
		obs.RecordQQMediaRelay(ctx, "download", "failed", 0)
		return "", fmt.Errorf("QQ媒体文件为空")
	}

	keep = true
	obs.RecordQQMediaRelay(ctx, "download", "ok", n)
	logger.Info("QQ媒体文件已下载",
		logger.StringField("path", localPath),
		logger.Int64Field("size", n))
	return localPath, nil
}

// UploadToMinIO 上传文件到 MinIO，返回网关可访问的稳定代理 URL。
//
// 对象键不包含原始文件名：原始名既可能重复，也可能带路径分隔符；
// 这里只保留白名单媒体扩展名，其余一律按内容嗅探决定。
func (m *MediaRelay) UploadToMinIO(ctx context.Context, localPath string) (string, json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if localPath == "" {
		return "", nil, fmt.Errorf("本地文件路径为空")
	}

	file, err := os.Open(localPath)
	if err != nil {
		return "", nil, fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("读取文件信息失败: %w", err)
	}
	maxBytes := m.maxBytes
	if maxBytes <= 0 {
		maxBytes = maxQQMediaBytes
	}
	if info.Size() <= 0 {
		return "", nil, fmt.Errorf("QQ媒体文件为空")
	}
	if info.Size() > maxBytes {
		obs.RecordQQMediaRelay(ctx, "upload", "too_large", info.Size())
		return "", nil, fmt.Errorf("%w: QQ媒体大小 %d 超过 %d", uploadguard.ErrTooLarge, info.Size(), maxBytes)
	}

	head := make([]byte, 512)
	n, readErr := io.ReadFull(file, head)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return "", nil, fmt.Errorf("读取文件头失败: %w", readErr)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", nil, fmt.Errorf("重置文件读取位置失败: %w", err)
	}

	ext := uploadguard.SanitizeExt(info.Name())
	contentType := detectMediaContentType(head[:n], ext)
	if mediaExt := mediaExtension(contentType); mediaExt != "" {
		ext = mediaExt
	}
	if ext == "" {
		ext = ".bin"
	}

	objectKey := fmt.Sprintf("%s/%s/%s%s",
		qqMediaObjectPrefix, m.now().Format("2006/01/02"), uuid.NewString(), ext)

	store, err := m.ensureStore()
	if err != nil {
		obs.RecordQQMediaRelay(ctx, "upload", "failed", info.Size())
		return "", nil, err
	}
	bucket := m.cfg.Minio.Bucket
	if bucket == "" {
		// 与网关 NewFileUploadHandler 的默认桶保持一致；
		// 两边默认值不同会出现「上传成功、代理 404」。
		bucket = defaultMediaBucket
	}
	if err := store.UploadFileContext(ctx, bucket, objectKey, file, info.Size(), contentType); err != nil {
		obs.RecordQQMediaRelay(ctx, "upload", relayResult(ctx, err), info.Size())
		return "", nil, fmt.Errorf("上传 QQ 媒体到 MinIO 失败: %w", err)
	}

	obs.RecordQQMediaRelay(ctx, "upload", "ok", info.Size())
	logger.Info("QQ媒体已上传 MinIO",
		logger.StringField("object_key", objectKey),
		logger.StringField("content_type", contentType),
		logger.Int64Field("size", info.Size()))

	meta, err := json.Marshal(map[string]any{
		"object_key":  objectKey,
		"size":        info.Size(),
		"mime_type":   contentType,
		"relay":       "qqbridge",
		"uploaded_at": m.now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return "", nil, fmt.Errorf("序列化媒体元数据失败: %w", err)
	}
	return "/api/v1/file/minio/" + objectKey, meta, nil
}

// SweepTempDir 清理上次进程崩溃遗留的临时媒体文件。
//
// 正常路径每条消息都会 defer Cleanup，但进程被 kill/OOM 时 defer 不会执行；
// Bridge 是常驻进程，长期不清理会把磁盘慢慢吃满。只删除超过 maxAge 的文件，
// 避免误删正在进行中的下载。
func (m *MediaRelay) SweepTempDir(ctx context.Context, maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	entries, err := os.ReadDir(m.tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取QQ媒体临时目录失败: %w", err)
	}

	removed := 0
	cutoff := m.now().Add(-maxAge)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(m.tempDir, entry.Name())); err != nil {
			logger.Warn("清理QQ媒体临时文件失败",
				logger.StringField("path", entry.Name()),
				logger.ErrorField(err))
			continue
		}
		removed++
	}
	if removed > 0 {
		logger.Info("已清理QQ媒体临时文件", logger.IntField("count", removed))
	}
	return removed, nil
}

// relayResult 区分「调用方主动取消」和「后端真的失败」。
// 两者混在一个 failed 里，会让用户关闭页面被误判成媒体服务故障。
func relayResult(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	if errors.Is(err, uploadguard.ErrTooLarge) {
		return "too_large"
	}
	return "failed"
}

func (m *MediaRelay) ensureStore() (mediaUploader, error) {
	m.storeMu.Lock()
	defer m.storeMu.Unlock()

	if m.store != nil {
		return m.store, nil
	}
	client, err := storage.InitMinio()
	if err != nil {
		return nil, fmt.Errorf("初始化 MinIO 失败: %w", err)
	}
	m.store = storage.NewMinioManager(client)
	return m.store, nil
}

// RelayInboundMedia 尝试把第一条带 URL 的媒体转存到 MinIO。
//
// 失败只返回 error，由调用方决定是否降级；消息文本本身已经由 converter
// 写成「[图片]」等占位，媒体中转失败不会让整条消息丢失。
func (m *MediaRelay) RelayInboundMedia(ctx context.Context, event *types.MessageEvent, msg *InboundMessage) error {
	if event == nil || msg == nil || event.MediaURL != "" {
		return nil
	}
	seg, ok := firstMediaSegment(msg)
	if !ok || seg.Data.URL == "" {
		return nil
	}

	// 并发闸门和消息预算共用同一个 ctx：排队等太久就放弃媒体，
	// 保留文本占位，不能让一条图片消息无限期占住 Bridge 的处理协程。
	if err := m.acquire(ctx); err != nil {
		// 排队失败不能只留一行日志：它说明容量不足，而消息本身仍然成功，
		// 在消息成功率上完全隐形。stage=queue 与下载/上传失败区分开。
		obs.RecordQQMediaRelay(ctx, "queue", relayResult(ctx, err), 0)
		return fmt.Errorf("等待媒体中转槽位失败: %w", err)
	}
	defer m.release()

	localPath, err := m.DownloadFromQQ(ctx, seg.Data.URL, seg.Data.File)
	if err != nil {
		return err
	}
	defer m.Cleanup(localPath)

	mediaURL, mediaMeta, err := m.UploadToMinIO(ctx, localPath)
	if err != nil {
		return err
	}
	event.MediaURL = mediaURL
	event.MediaMeta = mediaMeta
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["qq_media_relayed"] = "true"
	return nil
}

func (m *MediaRelay) acquire(ctx context.Context) error {
	if m.sem == nil {
		return nil
	}
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *MediaRelay) release() {
	if m.sem == nil {
		return
	}
	<-m.sem
}

func firstMediaSegment(msg *InboundMessage) (OneBotSegment, bool) {
	if msg == nil {
		return OneBotSegment{}, false
	}
	for _, seg := range msg.Message {
		switch seg.Type {
		case "image", "record", "video", "file":
			if seg.Data.URL != "" {
				return seg, true
			}
		}
	}
	return OneBotSegment{}, false
}

// detectMediaContentType 只把图片/音频/视频标成对应类型。
// 其他内容（包括 text/html）统一 application/octet-stream，避免媒体代理变成 XSS 入口。
func detectMediaContentType(head []byte, ext string) string {
	sniffed := http.DetectContentType(head)
	if isSafeMediaContentType(sniffed) {
		return sniffed
	}
	if sniffed == "application/octet-stream" {
		if byExt := safeMediaTypeByExt(ext); byExt != "" {
			return byExt
		}
	}
	return "application/octet-stream"
}

func isSafeMediaContentType(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "audio/") ||
		strings.HasPrefix(contentType, "video/")
}

func safeMediaTypeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return ""
	}
}

func mediaExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		return ""
	}
}

// Cleanup 清理临时文件。
func (m *MediaRelay) Cleanup(localPath string) {
	if localPath != "" {
		if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
			logger.Warn("清理临时文件失败", logger.StringField("path", localPath), logger.ErrorField(err))
		}
	}
}
