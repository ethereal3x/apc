package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewS3Client_NilConfig 验证空配置会返回配置错误
func TestNewS3Client_NilConfig(t *testing.T) {
	client, err := NewS3Client(context.Background(), NewS3ClientParams{Provider: STORAGE_PROVIDER_MINIO})
	assert.Nil(t, client)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

// TestNewS3Client_MissingRequiredFields 验证缺少必填配置会返回配置错误
func TestNewS3Client_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"empty_endpoint", &Config{AccessKey: "key", SecretKey: "secret"}},
		{"empty_access_key", &Config{Endpoint: "localhost:9000", SecretKey: "secret"}},
		{"empty_secret_key", &Config{Endpoint: "localhost:9000", AccessKey: "key"}},
		{"empty_bucket", &Config{Endpoint: "localhost:9000", AccessKey: "key", SecretKey: "secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewS3Client(context.Background(), NewS3ClientParams{
				Provider: STORAGE_PROVIDER_MINIO,
				Config:   tc.cfg,
			})
			assert.Nil(t, client)
			assert.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
}

// TestS3Client_UploadInvalidObject 验证上传参数非法时不会进入 SDK 调用
func TestS3Client_UploadInvalidObject(t *testing.T) {
	client := &S3Client{provider: STORAGE_PROVIDER_MINIO}
	cases := []struct {
		name       string
		objectName string
		reader     io.Reader
		size       int64
	}{
		{name: "empty_object_name", reader: bytes.NewReader([]byte("payload")), size: 7},
		{name: "nil_reader", objectName: "avatar/a.jpg", size: 7},
		{name: "negative_size", objectName: "avatar/a.jpg", reader: bytes.NewReader([]byte("payload")), size: -1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := client.Upload(context.Background(), testCase.objectName, testCase.reader, testCase.size, UploadOptions{})
			require.True(t, errors.Is(err, ErrInvalidObject))
		})
	}
}

// TestS3Client_MultipartInvalidParams 验证分片上传参数非法时不会进入 SDK 调用
func TestS3Client_MultipartInvalidParams(t *testing.T) {
	client := &S3Client{provider: STORAGE_PROVIDER_MINIO}
	t.Run("init_empty_object_name", func(t *testing.T) {
		_, err := client.InitiateMultipartUpload(context.Background(), InitiateMultipartUploadParams{})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
	t.Run("upload_part_empty_upload_id", func(t *testing.T) {
		_, err := client.UploadPart(context.Background(), UploadPartParams{
			ObjectName: "video/a.mp4",
			Reader:     bytes.NewReader([]byte("payload")),
			Size:       7,
			PartNumber: 1,
		})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
	t.Run("upload_part_invalid_part_number", func(t *testing.T) {
		_, err := client.UploadPart(context.Background(), UploadPartParams{
			ObjectName: "video/a.mp4",
			UploadID:   "upload-id",
			Reader:     bytes.NewReader([]byte("payload")),
			Size:       7,
			PartNumber: 10001,
		})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
	t.Run("complete_empty_parts", func(t *testing.T) {
		_, err := client.CompleteMultipartUpload(context.Background(), CompleteMultipartUploadParams{
			ObjectName: "video/a.mp4",
			UploadID:   "upload-id",
		})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
	t.Run("complete_invalid_part", func(t *testing.T) {
		_, err := client.CompleteMultipartUpload(context.Background(), CompleteMultipartUploadParams{
			ObjectName: "video/a.mp4",
			UploadID:   "upload-id",
			Parts:      []UploadedPart{{PartNumber: 1}},
		})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
	t.Run("abort_empty_upload_id", func(t *testing.T) {
		err := client.AbortMultipartUpload(context.Background(), AbortMultipartUploadParams{ObjectName: "video/a.mp4"})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
	t.Run("progress_empty_upload_id", func(t *testing.T) {
		_, err := client.GetMultipartUploadProgress(context.Background(), MultipartUploadProgressParams{ObjectName: "video/a.mp4"})
		require.True(t, errors.Is(err, ErrInvalidObject))
	})
}

// TestS3Client_UploadPartCanceled 验证取消信号会中断分片上传
func TestS3Client_UploadPartCanceled(t *testing.T) {
	cancelChan := make(chan struct{})
	close(cancelChan)
	client := &S3Client{provider: STORAGE_PROVIDER_MINIO}

	_, err := client.UploadPart(context.Background(), UploadPartParams{
		ObjectName: "video/a.mp4",
		UploadID:   "upload-id",
		PartNumber: 1,
		Reader:     bytes.NewReader([]byte("payload")),
		Size:       7,
		CancelChan: cancelChan,
	})

	require.True(t, errors.Is(err, ErrUploadCanceled))
}

// TestS3Client_NotifyMultipartProgress 验证分片上传进度事件
func TestS3Client_NotifyMultipartProgress(t *testing.T) {
	progressChan := make(chan MultipartUploadProgress, 1)
	client := &S3Client{provider: STORAGE_PROVIDER_MINIO}

	client.notifyMultipartProgress(UploadPartParams{
		ObjectName:         "video/a.mp4",
		UploadID:           "upload-id",
		TotalSize:          100,
		UploadedSizeBefore: 40,
		ProgressChan:       progressChan,
	}, UploadedPart{PartNumber: 2, ETag: "etag", Size: 10})

	progress := <-progressChan
	require.Equal(t, "video/a.mp4", progress.ObjectName)
	require.Equal(t, "upload-id", progress.UploadID)
	require.Equal(t, 2, progress.PartNumber)
	require.Equal(t, int64(10), progress.PartSize)
	require.Equal(t, int64(50), progress.UploadedSize)
	require.Equal(t, float64(50), progress.Percent)
}

// TestS3Client_PublicURL 验证公共 URL 会正确转义对象路径
func TestS3Client_PublicURL(t *testing.T) {
	client := &S3Client{
		provider:   STORAGE_PROVIDER_MINIO,
		bucketName: "avatar",
		endpoint:   "storage.example.com",
		useSSL:     true,
	}

	publicURL := client.PublicURL("用户 1/avatar#1.png")

	assert.Equal(t, "https://storage.example.com/avatar/%E7%94%A8%E6%88%B7%201/avatar%231.png", publicURL)
}

// TestS3Client_DefaultRegion 验证 S3客户端会补齐默认签名 region
func TestS3Client_DefaultRegion(t *testing.T) {
	client, err := newS3ClientWithContext(context.Background(), newS3ClientParams{
		Provider: STORAGE_PROVIDER_RUSTFS,
		Config: &Config{
			Endpoint:   "localhost:9000",
			AccessKey:  "key",
			SecretKey:  "secret",
			BucketName: "bucket",
		},
	})

	require.NoError(t, err)
	assert.NotNil(t, client)
}

// TestNewS3Client_UnsupportedProvider 验证工厂会拒绝不支持的 provider
func TestNewS3Client_UnsupportedProvider(t *testing.T) {
	client, err := NewS3Client(context.Background(), NewS3ClientParams{
		Provider: StorageProvider("unknown"),
		Config: &Config{
			Endpoint:   "localhost:9000",
			AccessKey:  "key",
			SecretKey:  "secret",
			BucketName: "bucket",
		},
	})

	assert.Nil(t, client)
	assert.Error(t, err)
}

// TestNewS3Client_MinioProvider 验证 S3 客户端会保留 provider 标识
func TestNewS3Client_MinioProvider(t *testing.T) {
	client, err := newS3ClientWithContext(context.Background(), newS3ClientParams{
		Provider: STORAGE_PROVIDER_MINIO,
		Config: &Config{
			Endpoint:   "localhost:9000",
			AccessKey:  "key",
			SecretKey:  "secret",
			BucketName: "bucket",
		},
	})

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, STORAGE_PROVIDER_MINIO, client.provider)
}

// TestS3EndpointURL 验证 S3 endpoint 会按 SSL 配置补齐 scheme
func TestS3EndpointURL(t *testing.T) {
	assert.Equal(t, "http://rustfs.example.com", s3EndpointURL(&Config{Endpoint: "rustfs.example.com"}))
	assert.Equal(t, "https://rustfs.example.com", s3EndpointURL(&Config{Endpoint: "rustfs.example.com", UseSSL: true}))
	assert.Equal(t, "http://custom.example.com", s3EndpointURL(&Config{Endpoint: "http://custom.example.com", UseSSL: true}))
}

// TestS3CompletedParts 验证 S3 合并分片参数排序和校验
func TestS3CompletedParts(t *testing.T) {
	parts, size, err := completedMultipartParts(CompleteMultipartUploadParams{
		ObjectName: "video/a.mp4",
		UploadID:   "upload-id",
		Parts: []UploadedPart{
			{PartNumber: 2, ETag: "etag-2", Size: 20},
			{PartNumber: 1, ETag: "etag-1", Size: 10},
		},
	})

	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, int32(1), *parts[0].PartNumber)
	assert.Equal(t, "etag-1", *parts[0].ETag)
	assert.Equal(t, int32(2), *parts[1].PartNumber)
	assert.Equal(t, int64(30), size)
}

// TestS3Client_E2E 完整流程集成测试：上传→下载→MD5校验→预签名URL→删除
// 通过环境变量驱动，未配置时跳过：
//
//	MINIO_VPS_ENDPOINT, MINIO_VPS_ACCESS_KEY, MINIO_VPS_SECRET_KEY, MINIO_VPS_IMAGE
//	MINIO_VPS_BUCKET (可选, 默认 vps-test-bucket)
//	MINIO_VPS_USE_SSL (可选, 默认 false)
func TestS3Client_E2E(t *testing.T) {
	cfg, imgPath := e2eConfigFromEnv(t)
	client, err := NewS3Client(context.Background(), NewS3ClientParams{
		Provider: STORAGE_PROVIDER_MINIO,
		Config:   cfg,
	})
	require.NoError(t, err, "init client and bucket check")

	payload, err := os.ReadFile(imgPath)
	require.NoError(t, err, "read image %s", imgPath)
	ext := filepath.Ext(imgPath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	objectName := fmt.Sprintf("e2e-test/image-%d%s", time.Now().UnixNano(), ext)
	originHash := md5.Sum(payload)
	t.Logf("payload: object=%s size=%d md5=%x", objectName, len(payload), originHash)

	ctx := context.Background()
	t.Run("Upload", func(t *testing.T) {
		err := client.Upload(ctx, objectName, bytes.NewReader(payload), int64(len(payload)),
			UploadOptions{ContentType: contentType})
		require.NoError(t, err)
	})

	t.Run("Download_MD5_Match", func(t *testing.T) {
		rc, err := client.Download(ctx, objectName)
		require.NoError(t, err)
		defer rc.Close()
		downloaded, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, len(payload), len(downloaded), "size mismatch")
		assert.Equal(t, originHash, md5.Sum(downloaded), "md5 mismatch")
	})

	t.Run("PresignedGetURL_Accessible", func(t *testing.T) {
		url, err := client.PresignedGetURL(ctx, objectName, time.Hour)
		require.NoError(t, err)
		t.Logf("presigned GET url: %s", url)
		assert.Contains(t, url, "X-Amz-Signature")
		resp, err := http.Get(url)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, originHash, md5.Sum(body), "md5 mismatch via presigned url")
	})

	t.Run("PresignedPutURL_Generated", func(t *testing.T) {
		url, err := client.PresignedPutURL(ctx, "e2e-test/presigned-upload.bin", time.Hour)
		require.NoError(t, err)
		t.Logf("presigned PUT url: %s", url)
		assert.Contains(t, url, "X-Amz-Signature")
	})

	t.Run("Remove", func(t *testing.T) {
		if os.Getenv("MINIO_VPS_KEEP") == "true" {
			url, err := client.PresignedGetURL(ctx, objectName, 24*time.Hour)
			require.NoError(t, err)
			t.Logf("MINIO_VPS_KEEP=true，跳过删除")
			t.Logf("对象保留，24小时有效访问链接: %s", url)
			t.Skip()
		}
		err := client.Remove(ctx, objectName)
		require.NoError(t, err)
	})
}

// e2eConfigFromEnv 从环境变量构建 e2e 测试配置和待上传图片路径，缺失关键变量时跳过测试
func e2eConfigFromEnv(t *testing.T) (*Config, string) {
	t.Helper()
	endpoint := os.Getenv("MINIO_VPS_ENDPOINT")
	ak := os.Getenv("MINIO_VPS_ACCESS_KEY")
	sk := os.Getenv("MINIO_VPS_SECRET_KEY")
	imgPath := os.Getenv("MINIO_VPS_IMAGE")
	if endpoint == "" || ak == "" || sk == "" || imgPath == "" {
		t.Skip("MinIO e2e 环境变量未配置，跳过 (需要 MINIO_VPS_ENDPOINT/ACCESS_KEY/SECRET_KEY/IMAGE)")
	}
	bucket := os.Getenv("MINIO_VPS_BUCKET")
	if bucket == "" {
		bucket = "vps-test-bucket"
	}
	cfg := &Config{
		Endpoint:   endpoint,
		AccessKey:  ak,
		SecretKey:  sk,
		BucketName: bucket,
		UseSSL:     os.Getenv("MINIO_VPS_USE_SSL") == "true",
	}
	return cfg, imgPath
}

// TestS3Client_PublicBucket_E2E 公共 bucket 流程：创建桶→设置匿名读→上传→匿名访问 PublicURL→清理
// 适用于头像、静态资源等场景，URL 永久有效（直到对象被删）
func TestS3Client_PublicBucket_E2E(t *testing.T) {
	cfg, imgPath := e2eConfigFromEnv(t)
	publicBucket := os.Getenv("MINIO_VPS_PUBLIC_BUCKET")
	if publicBucket == "" {
		publicBucket = fmt.Sprintf("e2e-public-%d", time.Now().Unix())
	}
	cfg.BucketName = publicBucket

	adminClient, err := newS3ClientWithContext(context.Background(), newS3ClientParams{
		Provider: STORAGE_PROVIDER_MINIO,
		Config:   cfg,
	})
	require.NoError(t, err)
	exists, err := adminClient.BucketExists(context.Background())
	require.NoError(t, err)
	if !exists {
		require.NoError(t, adminClient.MakeBucket(context.Background()))
		t.Logf("created public bucket: %s", publicBucket)
	}

	client, err := NewS3Client(context.Background(), NewS3ClientParams{
		Provider: STORAGE_PROVIDER_MINIO,
		Config:   cfg,
	})
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, client.SetBucketAnonymousReadOnly(ctx), "set anonymous read")

	payload, err := os.ReadFile(imgPath)
	require.NoError(t, err)
	ext := filepath.Ext(imgPath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	objectName := fmt.Sprintf("public-test/image-%d%s", time.Now().UnixNano(), ext)
	originHash := md5.Sum(payload)

	t.Run("Upload", func(t *testing.T) {
		err := client.Upload(ctx, objectName, bytes.NewReader(payload), int64(len(payload)),
			UploadOptions{ContentType: contentType})
		require.NoError(t, err)
	})

	t.Run("PublicURL_Anonymous_Access", func(t *testing.T) {
		url := client.PublicURL(objectName)
		t.Logf("public URL (永久有效): %s", url)
		assert.NotContains(t, url, "X-Amz-Signature", "PublicURL 不应该带签名参数")
		resp, err := http.Get(url)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "anonymous GET should succeed")
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, originHash, md5.Sum(body), "md5 mismatch via public url")
	})

	t.Run("Cleanup", func(t *testing.T) {
		if os.Getenv("MINIO_VPS_KEEP") == "true" {
			t.Logf("MINIO_VPS_KEEP=true，保留对象: %s", client.PublicURL(objectName))
			t.Skip()
		}
		require.NoError(t, client.Remove(ctx, objectName))
	})
}
