package storage

import (
	"context"
	"io"
	"time"
)

const storageDefaultInitTimeout = 5 * time.Second
const storageMaxMultipartPartNumber = 10000

// Config 对象存储连接和 bucket 配置
type Config struct {
	Endpoint   string `yaml:"endpoint" json:"endpoint"`
	AccessKey  string `yaml:"access_key" json:"access_key"`
	SecretKey  string `yaml:"secret_key" json:"secret_key"`
	BucketName string `yaml:"bucket_name" json:"bucket_name"`
	UseSSL     bool   `yaml:"use_ssl" json:"use_ssl"`
	Region     string `yaml:"region" json:"region"`
}

// validateStorageConfig 校验对象存储客户端初始化必填配置
func validateStorageConfig(cfg *Config) error {
	if cfg == nil || cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.BucketName == "" {
		return ErrInvalidConfig
	}
	return nil
}

// ObjectStorage 对象存储业务 API 抽象
type ObjectStorage interface {
	// Upload 上传对象到底层存储
	Upload(ctx context.Context, objectName string, reader io.Reader, size int64, opts UploadOptions) error

	// Download 下载对象，返回的 ReadCloser 必须由调用方关闭
	Download(ctx context.Context, objectName string) (io.ReadCloser, error)

	// Remove 删除指定对象
	Remove(ctx context.Context, objectName string) error

	// PublicURL 返回对象的永久公共 URL，前提是底层存储桶已开启匿名读
	PublicURL(objectName string) string

	// PresignedGetURL 生成预签名下载 URL，适用于私有资源临时授权访问
	PresignedGetURL(ctx context.Context, objectName string, expiry time.Duration) (string, error)

	// PresignedPutURL 生成预签名上传 URL，适用于前端直传场景
	PresignedPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error)

	// InitiateMultipartUpload 初始化分片上传任务
	InitiateMultipartUpload(ctx context.Context, params InitiateMultipartUploadParams) (MultipartUpload, error)

	// UploadPart 上传单个分片
	UploadPart(ctx context.Context, params UploadPartParams) (UploadedPart, error)

	// GetMultipartUploadProgress 获取分片上传当前进度
	GetMultipartUploadProgress(ctx context.Context, params MultipartUploadProgressParams) (MultipartUploadProgress, error)

	// CompleteMultipartUpload 合并已上传分片
	CompleteMultipartUpload(ctx context.Context, params CompleteMultipartUploadParams) (UploadResult, error)

	// AbortMultipartUpload 终止分片上传任务并清理已上传分片
	AbortMultipartUpload(ctx context.Context, params AbortMultipartUploadParams) error
}

// UploadOptions 上传选项，抽象掉具体厂商 SDK 的 PutObjectOptions
type UploadOptions struct {
	// ContentType MIME 类型，例如 image/jpeg
	ContentType string `json:"content_type"`

	// UserMetadata 用户自定义元数据，存储后端会以 x-amz-meta-* 头形式持久化
	UserMetadata map[string]string `json:"user_metadata"`

	// CacheControl HTTP Cache-Control 头，例如 "max-age=86400"
	CacheControl string `json:"cache_control"`
}

// InitiateMultipartUploadParams 初始化分片上传参数
type InitiateMultipartUploadParams struct {
	ObjectName string        `json:"object_name"`
	Options    UploadOptions `json:"options"`
}

// MultipartUpload 分片上传任务信息
type MultipartUpload struct {
	ObjectName string `json:"object_name"`
	UploadID   string `json:"upload_id"`
}

// UploadPartParams 上传单个分片参数
type UploadPartParams struct {
	ObjectName         string                         `json:"object_name"`
	UploadID           string                         `json:"upload_id"`
	PartNumber         int                            `json:"part_number"`
	Reader             io.Reader                      `json:"-"`
	Size               int64                          `json:"size"`
	TotalSize          int64                          `json:"total_size"`
	UploadedSizeBefore int64                          `json:"uploaded_size_before"`
	ProgressChan       chan<- MultipartUploadProgress `json:"-"`
	CancelChan         <-chan struct{}                `json:"-"`
}

// UploadedPart 已上传分片信息
type UploadedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
}

// MultipartUploadProgressParams 查询分片上传进度参数
type MultipartUploadProgressParams struct {
	ObjectName string `json:"object_name"`
	UploadID   string `json:"upload_id"`
	TotalSize  int64  `json:"total_size"`
}

// MultipartUploadProgress 分片上传进度
type MultipartUploadProgress struct {
	ObjectName    string         `json:"object_name"`
	UploadID      string         `json:"upload_id"`
	UploadedParts []UploadedPart `json:"uploaded_parts"`
	UploadedSize  int64          `json:"uploaded_size"`
	TotalSize     int64          `json:"total_size"`
	PartNumber    int            `json:"part_number"`
	PartSize      int64          `json:"part_size"`
	Completed     bool           `json:"completed"`
	Percent       float64        `json:"percent"`
}

// CompleteMultipartUploadParams 合并分片上传参数
type CompleteMultipartUploadParams struct {
	ObjectName string         `json:"object_name"`
	UploadID   string         `json:"upload_id"`
	Parts      []UploadedPart `json:"parts"`
	Options    UploadOptions  `json:"options"`
}

// UploadResult 上传完成后的对象信息
type UploadResult struct {
	BucketName string `json:"bucket_name"`
	ObjectName string `json:"object_name"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
	Location   string `json:"location"`
}

// AbortMultipartUploadParams 终止分片上传参数
type AbortMultipartUploadParams struct {
	ObjectName string `json:"object_name"`
	UploadID   string `json:"upload_id"`
}
