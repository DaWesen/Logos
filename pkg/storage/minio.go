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
	minioOnce   sync.Once
)

type MinioManager struct {
	client *minio.Client
}

func InitMinio() (*minio.Client, error) {
	var err error
	minioOnce.Do(func() {
		cfg := config.GetConfig()
		minioConfig := cfg.Minio

		minioClient, err = minio.New(minioConfig.Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(minioConfig.AccessKey, minioConfig.SecretKey, ""),
			Secure: minioConfig.Secure,
		})
		if err != nil {
			err = fmt.Errorf("failed to create minio client: %w", err)
			return
		}

		// 检查连接
		ok, err := minioClient.BucketExists(context.Background(), minioConfig.Bucket)
		if err != nil {
			err = fmt.Errorf("failed to check bucket: %w", err)
			return
		}
		if !ok {
			// 创建 bucket
			err = minioClient.MakeBucket(context.Background(), minioConfig.Bucket, minio.MakeBucketOptions{})
			if err != nil {
				// 检查是否是因为 bucket 已存在
				exists, existsErr := minioClient.BucketExists(context.Background(), minioConfig.Bucket)
				if existsErr != nil || !exists {
					err = fmt.Errorf("failed to create bucket: %w", err)
					return
				}
				err = nil
			}
		}
	})
	return minioClient, err
}

func NewMinioManager(client *minio.Client) *MinioManager {
	return &MinioManager{
		client: client,
	}
}

func (m *MinioManager) UploadFile(bucketName, objectName string, reader io.Reader, size int64, contentType string) error {
	_, err := m.client.PutObject(context.Background(), bucketName, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}
	return nil
}

func (m *MinioManager) DownloadFile(bucketName, objectName string) (io.Reader, error) {
	obj, err := m.client.GetObject(context.Background(), bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	return obj, nil
}

func (m *MinioManager) DeleteFile(bucketName, objectName string) error {
	err := m.client.RemoveObject(context.Background(), bucketName, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

func (m *MinioManager) GetFileURL(bucketName, objectName string, expiresIn int) (string, error) {
	reqParams := make(url.Values)
	url, err := m.client.PresignedGetObject(context.Background(), bucketName, objectName, time.Duration(expiresIn)*time.Second, reqParams)
	if err != nil {
		return "", fmt.Errorf("failed to get presigned url: %w", err)
	}
	return url.String(), nil
}

func (m *MinioManager) FileExists(bucketName, objectName string) (bool, error) {
	_, err := m.client.StatObject(context.Background(), bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
