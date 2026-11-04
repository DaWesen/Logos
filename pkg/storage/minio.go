package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"Logos/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	minioClient *minio.Client
	minioMu     sync.Mutex

	// 初始化失败不能永久缓存成 sync.Once 的「已完成」：那样第二次调用会返回
	// (nil, nil)，调用方以为成功，随后在 nil client 上 panic。
	// 这里只缓存失败结果一小段时间，避免每个请求都去探测一个还没起来的 MinIO，
	// 同时保留自愈能力——依赖恢复后下一次调用可以真正初始化成功。
	minioLastErr  error
	minioRetryAt  time.Time
	minioInitFunc = initMinioClient
	minioNow      = time.Now
)

const (
	// minioInitTimeout 限制启动探测（BucketExists/MakeBucket）的耗时。
	// 没有上限时，MinIO 地址不可达会让服务启动流程一直挂住。
	minioInitTimeout = 10 * time.Second
	// minioInitRetryInterval 是失败后的负缓存窗口。
	minioInitRetryInterval = 5 * time.Second
)

type MinioManager struct {
	client *minio.Client
}

// InitMinio 懒初始化 MinIO 客户端并确保桶存在。
//
// 并发调用被串行化；成功结果永久复用，失败结果只缓存 minioInitRetryInterval，
// 之后重新尝试。这样 compose 里 MinIO 晚于网关启动时，存储能力可以自愈，
// 而不是「第一次失败就永久不可用」。
func InitMinio() (*minio.Client, error) {
	minioMu.Lock()
	defer minioMu.Unlock()

	if minioClient != nil {
		return minioClient, nil
	}
	if minioLastErr != nil && minioNow().Before(minioRetryAt) {
		return nil, minioLastErr
	}

	client, err := minioInitFunc()
	if err != nil {
		minioLastErr = err
		minioRetryAt = minioNow().Add(minioInitRetryInterval)
		return nil, err
	}

	minioClient = client
	minioLastErr = nil
	return minioClient, nil
}

func initMinioClient() (*minio.Client, error) {
	cfg := config.GetConfig()
	minioConfig := cfg.Minio

	client, err := minio.New(minioConfig.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioConfig.AccessKey, minioConfig.SecretKey, ""),
		Secure: minioConfig.Secure,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create minio client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), minioInitTimeout)
	defer cancel()

	ok, err := client.BucketExists(ctx, minioConfig.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if ok {
		return client, nil
	}

	if err := client.MakeBucket(ctx, minioConfig.Bucket, minio.MakeBucketOptions{}); err != nil {
		// 并发初始化时另一个实例可能刚创建了同一个桶：再查一次，存在即视为成功。
		exists, existsErr := client.BucketExists(ctx, minioConfig.Bucket)
		if existsErr != nil || !exists {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}
	return client, nil
}

func NewMinioManager(client *minio.Client) *MinioManager {
	return &MinioManager{
		client: client,
	}
}

func (m *MinioManager) UploadFile(bucketName, objectName string, reader io.Reader, size int64, contentType string) error {
	return m.UploadFileContext(context.Background(), bucketName, objectName, reader, size, contentType)
}

// UploadFileContext 带取消/超时的上传。
//
// UploadFile 为了兼容既有调用方保留无 ctx 的签名，但内部不能再用
// context.Background() 把调用方预算架空：否则上层设了 90s 超时，
// MinIO 卡住时仍然会一直占着连接和临时文件，直到进程退出。
func (m *MinioManager) UploadFileContext(
	ctx context.Context,
	bucketName, objectName string,
	reader io.Reader,
	size int64,
	contentType string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := m.client.PutObject(ctx, bucketName, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}
	return nil
}

func (m *MinioManager) DownloadFile(bucketName, objectName string) (io.Reader, error) {
	return m.DownloadFileContext(context.Background(), bucketName, objectName)
}

// DownloadFileContext 带调用方 ctx 的下载。
//
// 返回的 reader 与 ctx 绑定：调用方必须读完并 Close 后再 cancel，
// 提前 cancel 会让后续 Read 失败。
func (m *MinioManager) DownloadFileContext(ctx context.Context, bucketName, objectName string) (io.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obj, err := m.client.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	return obj, nil
}

func (m *MinioManager) DeleteFile(bucketName, objectName string) error {
	return m.DeleteFileContext(context.Background(), bucketName, objectName)
}

func (m *MinioManager) DeleteFileContext(ctx context.Context, bucketName, objectName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := m.client.RemoveObject(ctx, bucketName, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

func (m *MinioManager) GetFileURL(bucketName, objectName string, expiresIn int) (string, error) {
	return m.GetFileURLContext(context.Background(), bucketName, objectName, expiresIn)
}

func (m *MinioManager) GetFileURLContext(ctx context.Context, bucketName, objectName string, expiresIn int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	reqParams := make(url.Values)
	url, err := m.client.PresignedGetObject(ctx, bucketName, objectName, time.Duration(expiresIn)*time.Second, reqParams)
	if err != nil {
		return "", fmt.Errorf("failed to get presigned url: %w", err)
	}
	return url.String(), nil
}

func (m *MinioManager) FileExists(bucketName, objectName string) (bool, error) {
	return m.FileExistsContext(context.Background(), bucketName, objectName)
}

func (m *MinioManager) FileExistsContext(ctx context.Context, bucketName, objectName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := m.client.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
