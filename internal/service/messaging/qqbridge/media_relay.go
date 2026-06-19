package qqbridge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"Logos/config"
	"Logos/pkg/logger"
)

// MediaRelay 媒体文件中转
type MediaRelay struct {
	cfg     *config.Config
	tempDir string
}

// NewMediaRelay 创建媒体中转器
func NewMediaRelay(cfg *config.Config) *MediaRelay {
	tempDir := cfg.QQBridge.TempDir
	if tempDir == "" {
		tempDir = os.TempDir() + "/qqbridge"
	}

	return &MediaRelay{
		cfg:     cfg,
		tempDir: tempDir,
	}
}

// DownloadFromQQ 从 QQ 下载媒体文件到临时目录
func (m *MediaRelay) DownloadFromQQ(ctx context.Context, url, filename string) (string, error) {
	if err := os.MkdirAll(m.tempDir, 0755); err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}

	localPath := filepath.Join(m.tempDir, fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filename))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}

	f, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	logger.Info("QQ媒体文件已下载", logger.StringField("path", localPath))
	return localPath, nil
}

// UploadToMinIO 上传文件到 MinIO
// TODO: 实现MinIO上传逻辑
func (m *MediaRelay) UploadToMinIO(ctx context.Context, localPath string) (string, error) {
	// 将在后续实现
	return "", fmt.Errorf("MinIO上传功能待实现")
}

// Cleanup 清理临时文件
func (m *MediaRelay) Cleanup(localPath string) {
	if localPath != "" {
		if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
			logger.Warn("清理临时文件失败", logger.StringField("path", localPath), logger.ErrorField(err))
		}
	}
}
