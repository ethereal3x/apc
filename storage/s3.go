package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	apctracing "github.com/ethereal3x/apc/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const storageDefaultRegion = "us-east-1"

const (
	STORAGE_PROVIDER_MINIO  StorageProvider = "minio"
	STORAGE_PROVIDER_RUSTFS StorageProvider = "rustfs"
)

// StorageProvider 对象存储实现类型
type StorageProvider string

// NewS3ClientParams S3 客户端初始化参数
type NewS3ClientParams struct {
	Provider StorageProvider
	Config   *Config
}

// newS3ClientParams S3 客户端内部初始化参数
type newS3ClientParams struct {
	Provider    StorageProvider
	Config      *Config
	CheckBucket bool
}

// buildMultipartProgressParams 分片上传进度构造参数
type buildMultipartProgressParams struct {
	ObjectName   string
	UploadID     string
	Parts        []UploadedPart
	UploadedSize int64
	TotalSize    int64
}

// putObjectInputParams PutObject 请求构造参数
type putObjectInputParams struct {
	ObjectName string
	Reader     io.Reader
	Size       int64
	Options    UploadOptions
}

// S3Client 封装 AWS S3 SDK，适配 MinIO、RustFS 等 S3 对象存储
type S3Client struct {
	s3Cli      *s3.Client
	preSigner  *s3.PresignClient
	bucketName string
	endpoint   string
	useSSL     bool
	provider   StorageProvider
}

// 编译期断言 *S3Client 实现 ObjectStorage 接口
var _ ObjectStorage = (*S3Client)(nil)

// NewS3Client 初始化 S3 客户端并验证 bucket 是否存在
func NewS3Client(ctx context.Context, params NewS3ClientParams) (*S3Client, error) {
	return newS3ClientWithContext(ctx, newS3ClientParams{
		Provider:    params.Provider,
		Config:      params.Config,
		CheckBucket: true,
	})
}

// newS3ClientWithContext 初始化 S3 客户端
func newS3ClientWithContext(ctx context.Context, params newS3ClientParams) (*S3Client, error) {
	cfg, err := normalizeS3Config(params)
	if err != nil {
		return nil, err
	}
	endpointURL := s3EndpointURL(cfg)
	awsConfig := aws.Config{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(endpointURL),
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
	}
	rawClient := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = true
	})
	fsc := &S3Client{
		s3Cli:      rawClient,
		preSigner:  s3.NewPresignClient(rawClient),
		bucketName: cfg.BucketName,
		endpoint:   cfg.Endpoint,
		useSSL:     cfg.UseSSL,
		provider:   params.Provider,
	}
	if !params.CheckBucket {
		return fsc, nil
	}
	exists, err := fsc.BucketExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: check %s bucket: %w", params.Provider, err)
	}
	if !exists {
		return nil, fmt.Errorf("storage: bucket %q: %w", cfg.BucketName, ErrBucketNotFound)
	}
	return fsc, nil
}

// normalizeS3Config 校验并补齐 S3客户端配置
func normalizeS3Config(params newS3ClientParams) (*Config, error) {
	switch params.Provider {
	case STORAGE_PROVIDER_MINIO, STORAGE_PROVIDER_RUSTFS:
	default:
		return nil, fmt.Errorf("storage: unsupported storage provider %q", params.Provider)
	}
	if err := validateStorageConfig(params.Config); err != nil {
		return nil, err
	}
	cfg := *params.Config
	if cfg.Region == "" {
		cfg.Region = storageDefaultRegion
	}
	return &cfg, nil
}

// useUnsignedPayload 允许 S3存储上传不可 seek 的流式请求体
func useUnsignedPayload(stack *middleware.Stack) error {
	return v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware(stack)
}

// withUnsignedPayload 为流式上传请求启用 unsigned payload
func withUnsignedPayload(options *s3.Options) {
	options.APIOptions = append(options.APIOptions, useUnsignedPayload)
}

// InitiateMultipartUpload 初始化分片上传任务
func (fsc *S3Client) InitiateMultipartUpload(ctx context.Context, params InitiateMultipartUploadParams) (MultipartUpload, error) {
	ctx, span := fsc.startStorageSpan(ctx, "multipart.initiate", params.ObjectName)
	defer span.End()
	if params.ObjectName == "" {
		err := fmt.Errorf("storage: object name is empty: %w", ErrInvalidObject)
		apctracing.RecordError(ctx, err)
		return MultipartUpload{}, err
	}
	input := fsc.newCreateMultipartUploadInput(params)
	result, err := fsc.s3Cli.CreateMultipartUpload(ctx, input)
	if err != nil {
		err = fmt.Errorf("storage: %s initiate multipart upload %q: %w", fsc.provider, params.ObjectName, err)
		apctracing.RecordError(ctx, err)
		return MultipartUpload{}, err
	}
	uploadID := aws.ToString(result.UploadId)
	span.SetAttributes(attribute.String("storage.multipart.upload_id", uploadID))
	return MultipartUpload{ObjectName: params.ObjectName, UploadID: uploadID}, nil
}

// UploadPart 上传单个分片
func (fsc *S3Client) UploadPart(ctx context.Context, params UploadPartParams) (UploadedPart, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := fsc.startStorageSpan(ctx, "multipart.upload_part", params.ObjectName)
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.multipart.upload_id", params.UploadID),
		attribute.Int("storage.multipart.part_number", params.PartNumber),
		attribute.Int64("storage.multipart.part_size", params.Size),
	)
	if params.CancelChan != nil {
		select {
		case <-params.CancelChan:
			err := fmt.Errorf("storage: %s multipart upload part %d canceled: %w", fsc.provider, params.PartNumber, ErrUploadCanceled)
			apctracing.RecordError(ctx, err)
			return UploadedPart{}, err
		default:
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelSignal := make(chan struct{})
	if params.CancelChan != nil {
		go func() {
			select {
			case <-params.CancelChan:
				close(cancelSignal)
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	if uploadCanceled(ctx, cancelSignal) {
		err := fmt.Errorf("storage: %s multipart upload part %d canceled: %w", fsc.provider, params.PartNumber, ErrUploadCanceled)
		apctracing.RecordError(ctx, err)
		return UploadedPart{}, err
	}
	if err := validateUploadPartParams(params); err != nil {
		apctracing.RecordError(ctx, err)
		return UploadedPart{}, err
	}
	result, err := fsc.s3Cli.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:        aws.String(fsc.bucketName),
		Key:           aws.String(params.ObjectName),
		UploadId:      aws.String(params.UploadID),
		PartNumber:    aws.Int32(int32(params.PartNumber)),
		Body:          params.Reader,
		ContentLength: aws.Int64(params.Size),
	}, withUnsignedPayload)
	if err != nil {
		if uploadCanceled(ctx, cancelSignal) {
			err = fmt.Errorf("storage: %s multipart upload part %d canceled: %w", fsc.provider, params.PartNumber, ErrUploadCanceled)
			apctracing.RecordError(ctx, err)
			return UploadedPart{}, err
		}
		err = fmt.Errorf("storage: %s upload part %d for %q: %w", fsc.provider, params.PartNumber, params.ObjectName, err)
		apctracing.RecordError(ctx, err)
		return UploadedPart{}, err
	}
	uploadedPart := UploadedPart{PartNumber: params.PartNumber, ETag: strings.Trim(aws.ToString(result.ETag), `"`), Size: params.Size}
	fsc.notifyMultipartProgress(params, uploadedPart)
	return uploadedPart, nil
}

// GetMultipartUploadProgress 获取分片上传当前进度
func (fsc *S3Client) GetMultipartUploadProgress(ctx context.Context, params MultipartUploadProgressParams) (MultipartUploadProgress, error) {
	ctx, span := fsc.startStorageSpan(ctx, "multipart.progress", params.ObjectName)
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.multipart.upload_id", params.UploadID),
		attribute.Int64("storage.multipart.total_size", params.TotalSize),
	)
	if params.ObjectName == "" || params.UploadID == "" {
		err := fmt.Errorf("storage: invalid %s multipart progress params: %w", fsc.provider, ErrInvalidObject)
		apctracing.RecordError(ctx, err)
		return MultipartUploadProgress{}, err
	}
	uploadedParts, uploadedSize, err := fsc.listUploadedParts(ctx, params)
	if err != nil {
		err = fmt.Errorf("storage: %s get multipart progress %q: %w", fsc.provider, params.ObjectName, err)
		apctracing.RecordError(ctx, err)
		return MultipartUploadProgress{}, err
	}
	progress := buildMultipartProgress(buildMultipartProgressParams{
		ObjectName:   params.ObjectName,
		UploadID:     params.UploadID,
		Parts:        uploadedParts,
		UploadedSize: uploadedSize,
		TotalSize:    params.TotalSize,
	})
	span.SetAttributes(
		attribute.Int64("storage.multipart.uploaded_size", progress.UploadedSize),
		attribute.Float64("storage.multipart.percent", progress.Percent),
	)
	return progress, nil
}

// CompleteMultipartUpload 合并已上传分片
func (fsc *S3Client) CompleteMultipartUpload(ctx context.Context, params CompleteMultipartUploadParams) (UploadResult, error) {
	ctx, span := fsc.startStorageSpan(ctx, "multipart.complete", params.ObjectName)
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.multipart.upload_id", params.UploadID),
		attribute.Int("storage.multipart.part_count", len(params.Parts)),
	)
	completedParts, size, err := completedMultipartParts(params)
	if err != nil {
		apctracing.RecordError(ctx, err)
		return UploadResult{}, err
	}
	result, err := fsc.s3Cli.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(fsc.bucketName),
		Key:      aws.String(params.ObjectName),
		UploadId: aws.String(params.UploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		err = fmt.Errorf("storage: %s complete multipart upload %q: %w", fsc.provider, params.ObjectName, err)
		apctracing.RecordError(ctx, err)
		return UploadResult{}, err
	}
	return UploadResult{
		BucketName: fsc.bucketName,
		ObjectName: params.ObjectName,
		ETag:       strings.Trim(aws.ToString(result.ETag), `"`),
		Size:       size,
		Location:   aws.ToString(result.Location),
	}, nil
}

// AbortMultipartUpload 终止分片上传任务并清理已上传分片
func (fsc *S3Client) AbortMultipartUpload(ctx context.Context, params AbortMultipartUploadParams) error {
	ctx, span := fsc.startStorageSpan(ctx, "multipart.abort", params.ObjectName)
	defer span.End()
	span.SetAttributes(attribute.String("storage.multipart.upload_id", params.UploadID))
	if params.ObjectName == "" || params.UploadID == "" {
		err := fmt.Errorf("storage: invalid %s multipart abort params: %w", fsc.provider, ErrInvalidObject)
		apctracing.RecordError(ctx, err)
		return err
	}
	if _, err := fsc.s3Cli.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(fsc.bucketName),
		Key:      aws.String(params.ObjectName),
		UploadId: aws.String(params.UploadID),
	}); err != nil {
		err = fmt.Errorf("storage: %s abort multipart upload %q: %w", fsc.provider, params.ObjectName, err)
		apctracing.RecordError(ctx, err)
		return err
	}
	return nil
}

// Upload 上传对象到 bucket
func (fsc *S3Client) Upload(ctx context.Context, objectName string, reader io.Reader, size int64, opts UploadOptions) error {
	ctx, span := fsc.startStorageSpan(ctx, "upload", objectName)
	defer span.End()
	span.SetAttributes(
		attribute.Int64("storage.object.size", size),
		attribute.String("storage.content_type", opts.ContentType),
	)
	if objectName == "" {
		err := fmt.Errorf("storage: object name is empty: %w", ErrInvalidObject)
		apctracing.RecordError(ctx, err)
		return err
	}
	if reader == nil || size < 0 {
		err := fmt.Errorf("storage: object %q payload is invalid: %w", objectName, ErrInvalidObject)
		apctracing.RecordError(ctx, err)
		return err
	}
	input := fsc.newPutObjectInput(putObjectInputParams{
		ObjectName: objectName,
		Reader:     reader,
		Size:       size,
		Options:    opts,
	})
	if _, err := fsc.s3Cli.PutObject(ctx, input, withUnsignedPayload); err != nil {
		err = fmt.Errorf("storage: %s upload %q: %w", fsc.provider, objectName, err)
		apctracing.RecordError(ctx, err)
		return err
	}
	return nil
}

// Download 从 bucket 下载对象
func (fsc *S3Client) Download(ctx context.Context, objectName string) (io.ReadCloser, error) {
	ctx, span := fsc.startStorageSpan(ctx, "download", objectName)
	defer span.End()
	result, err := fsc.s3Cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(fsc.bucketName),
		Key:    aws.String(objectName),
	})
	if err != nil {
		err = fmt.Errorf("storage: %s download %q: %w", fsc.provider, objectName, err)
		apctracing.RecordError(ctx, err)
		return nil, err
	}
	return result.Body, nil
}

// PublicURL 拼接对象的永久公共 URL
func (fsc *S3Client) PublicURL(objectName string) string {
	_, span := fsc.startStorageSpan(context.Background(), "public_url", objectName)
	defer span.End()
	scheme := "http"
	if fsc.useSSL {
		scheme = "https"
	}
	publicURL := url.URL{
		Scheme: scheme,
		Host:   fsc.endpoint,
		Path:   joinObjectPath(fsc.bucketName, objectName),
	}
	return publicURL.String()
}

// PresignedGetURL 生成预签名下载 URL
func (fsc *S3Client) PresignedGetURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	ctx, span := fsc.startStorageSpan(ctx, "presigned_get", objectName)
	defer span.End()
	span.SetAttributes(attribute.Int64("storage.presigned.expiry_ms", expiry.Milliseconds()))
	result, err := fsc.preSigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(fsc.bucketName),
		Key:    aws.String(objectName),
	}, func(options *s3.PresignOptions) {
		options.Expires = expiry
	})
	if err != nil {
		err = fmt.Errorf("storage: %s presigned get %q: %w", fsc.provider, objectName, err)
		apctracing.RecordError(ctx, err)
		return "", err
	}
	return result.URL, nil
}

// PresignedPutURL 生成预签名上传 URL
func (fsc *S3Client) PresignedPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	ctx, span := fsc.startStorageSpan(ctx, "presigned_put", objectName)
	defer span.End()
	span.SetAttributes(attribute.Int64("storage.presigned.expiry_ms", expiry.Milliseconds()))
	result, err := fsc.preSigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(fsc.bucketName),
		Key:    aws.String(objectName),
	}, func(options *s3.PresignOptions) {
		options.Expires = expiry
	})
	if err != nil {
		err = fmt.Errorf("storage: %s presigned put %q: %w", fsc.provider, objectName, err)
		apctracing.RecordError(ctx, err)
		return "", err
	}
	return result.URL, nil
}

// Remove 删除指定对象
func (fsc *S3Client) Remove(ctx context.Context, objectName string) error {
	ctx, span := fsc.startStorageSpan(ctx, "remove", objectName)
	defer span.End()
	if _, err := fsc.s3Cli.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(fsc.bucketName),
		Key:    aws.String(objectName),
	}); err != nil {
		err = fmt.Errorf("storage: %s remove %q: %w", fsc.provider, objectName, err)
		apctracing.RecordError(ctx, err)
		return err
	}
	return nil
}

// BucketExists 检查配置的 bucket 是否存在
func (fsc *S3Client) BucketExists(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, errors.New("storage: context is nil")
	}
	ctx, span := fsc.startStorageSpan(ctx, "bucket_exists", "")
	defer span.End()
	if _, err := fsc.s3Cli.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(fsc.bucketName)}); err != nil {
		if s3BucketNotFound(err) {
			span.SetAttributes(attribute.Bool("storage.bucket.exists", false))
			return false, nil
		}
		err = fmt.Errorf("storage: %s bucket exists %q: %w", fsc.provider, fsc.bucketName, err)
		apctracing.RecordError(ctx, err)
		return false, err
	}
	span.SetAttributes(attribute.Bool("storage.bucket.exists", true))
	return true, nil
}

// MakeBucket 创建 bucket
func (fsc *S3Client) MakeBucket(ctx context.Context) error {
	ctx, span := fsc.startStorageSpan(ctx, "make_bucket", "")
	defer span.End()
	if _, err := fsc.s3Cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(fsc.bucketName)}); err != nil {
		err = fmt.Errorf("storage: %s make bucket %q: %w", fsc.provider, fsc.bucketName, err)
		apctracing.RecordError(ctx, err)
		return err
	}
	return nil
}

// SetBucketAnonymousReadOnly 把当前 bucket 设为匿名公共只读
func (fsc *S3Client) SetBucketAnonymousReadOnly(ctx context.Context) error {
	ctx, span := fsc.startStorageSpan(ctx, "set_bucket_anonymous_read_only", "")
	defer span.End()
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":    "Allow",
			"Principal": "*",
			"Action":    []string{"s3:GetObject"},
			"Resource":  []string{fmt.Sprintf("arn:aws:s3:::%s/*", fsc.bucketName)},
		}},
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		err = fmt.Errorf("storage: marshal policy: %w", err)
		apctracing.RecordError(ctx, err)
		return err
	}
	if _, err := fsc.s3Cli.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(fsc.bucketName),
		Policy: aws.String(string(raw)),
	}); err != nil {
		err = fmt.Errorf("storage: %s set bucket policy %q: %w", fsc.provider, fsc.bucketName, err)
		apctracing.RecordError(ctx, err)
		return err
	}
	return nil
}

// notifyMultipartProgress 推送当前分片上传进度
func (fsc *S3Client) notifyMultipartProgress(params UploadPartParams, part UploadedPart) {
	if params.ProgressChan == nil {
		return
	}
	uploadedSize := params.UploadedSizeBefore + part.Size
	progress := buildMultipartProgress(buildMultipartProgressParams{
		ObjectName:   params.ObjectName,
		UploadID:     params.UploadID,
		Parts:        []UploadedPart{part},
		UploadedSize: uploadedSize,
		TotalSize:    params.TotalSize,
	})
	progress.PartNumber = part.PartNumber
	progress.PartSize = part.Size
	select {
	case params.ProgressChan <- progress:
	default:
	}
}

// newPutObjectInput 构造 PutObject 请求
func (fsc *S3Client) newPutObjectInput(params putObjectInputParams) *s3.PutObjectInput {
	input := &s3.PutObjectInput{
		Bucket:        aws.String(fsc.bucketName),
		Key:           aws.String(params.ObjectName),
		Body:          params.Reader,
		ContentLength: aws.Int64(params.Size),
		Metadata:      params.Options.UserMetadata,
		ContentType:   aws.String(params.Options.ContentType),
	}
	if params.Options.CacheControl != "" {
		input.CacheControl = aws.String(params.Options.CacheControl)
	}
	return input
}

// newCreateMultipartUploadInput 构造 CreateMultipartUpload 请求
func (fsc *S3Client) newCreateMultipartUploadInput(params InitiateMultipartUploadParams) *s3.CreateMultipartUploadInput {
	input := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(fsc.bucketName),
		Key:         aws.String(params.ObjectName),
		Metadata:    params.Options.UserMetadata,
		ContentType: aws.String(params.Options.ContentType),
	}
	if params.Options.CacheControl != "" {
		input.CacheControl = aws.String(params.Options.CacheControl)
	}
	return input
}

// listUploadedParts 分页读取已上传分片
func (fsc *S3Client) listUploadedParts(ctx context.Context, params MultipartUploadProgressParams) ([]UploadedPart, int64, error) {
	var uploadedParts []UploadedPart
	var uploadedSize int64
	var partNumberMarker *string
	for {
		result, err := fsc.s3Cli.ListParts(ctx, &s3.ListPartsInput{
			Bucket:           aws.String(fsc.bucketName),
			Key:              aws.String(params.ObjectName),
			UploadId:         aws.String(params.UploadID),
			MaxParts:         aws.Int32(1000),
			PartNumberMarker: partNumberMarker,
		})
		if err != nil {
			return nil, 0, err
		}
		for _, part := range result.Parts {
			uploadedPart := UploadedPart{
				PartNumber: int(aws.ToInt32(part.PartNumber)),
				ETag:       strings.Trim(aws.ToString(part.ETag), `"`),
				Size:       aws.ToInt64(part.Size),
			}
			uploadedParts = append(uploadedParts, uploadedPart)
			uploadedSize += uploadedPart.Size
		}
		if result.IsTruncated == nil || !aws.ToBool(result.IsTruncated) {
			break
		}
		partNumberMarker = result.NextPartNumberMarker
	}
	return uploadedParts, uploadedSize, nil
}

// buildMultipartProgress 构造分片上传进度
func buildMultipartProgress(params buildMultipartProgressParams) MultipartUploadProgress {
	progress := MultipartUploadProgress{
		ObjectName:    params.ObjectName,
		UploadID:      params.UploadID,
		UploadedParts: params.Parts,
		UploadedSize:  params.UploadedSize,
		TotalSize:     params.TotalSize,
	}
	if params.TotalSize > 0 {
		progress.Percent = float64(params.UploadedSize) * 100 / float64(params.TotalSize)
		progress.Completed = params.UploadedSize >= params.TotalSize
	}
	return progress
}

// uploadCanceled 判断上传是否已取消
func uploadCanceled(ctx context.Context, cancelSignal <-chan struct{}) bool {
	select {
	case <-cancelSignal:
		return true
	default:
	}
	return ctx.Err() != nil
}

// joinObjectPath 拼接 bucket 和对象路径
func joinObjectPath(bucketName string, objectName string) string {
	segments := []string{bucketName}
	for _, segment := range strings.Split(objectName, "/") {
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
	}
	return "/" + strings.Join(segments, "/")
}

// completedMultipartParts 校验并构造 CompleteMultipartUpload 分片列表
func completedMultipartParts(params CompleteMultipartUploadParams) ([]types.CompletedPart, int64, error) {
	if params.ObjectName == "" || params.UploadID == "" || len(params.Parts) == 0 {
		return nil, 0, fmt.Errorf("storage: invalid multipart complete params: %w", ErrInvalidObject)
	}
	sortedParts := append([]UploadedPart(nil), params.Parts...)
	sort.Slice(sortedParts, func(leftIndex, rightIndex int) bool {
		return sortedParts[leftIndex].PartNumber < sortedParts[rightIndex].PartNumber
	})
	completedParts := make([]types.CompletedPart, 0, len(sortedParts))
	var size int64
	for _, part := range sortedParts {
		if part.PartNumber <= 0 || part.PartNumber > storageMaxMultipartPartNumber || part.ETag == "" {
			return nil, 0, fmt.Errorf("storage: invalid multipart complete part: %w", ErrInvalidObject)
		}
		completedParts = append(completedParts, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(int32(part.PartNumber)),
		})
		size += part.Size
	}
	return completedParts, size, nil
}

// validateUploadPartParams 校验分片上传参数
func validateUploadPartParams(params UploadPartParams) error {
	if params.ObjectName == "" || params.UploadID == "" || params.PartNumber <= 0 || params.PartNumber > storageMaxMultipartPartNumber {
		return fmt.Errorf("storage: invalid multipart part params: %w", ErrInvalidObject)
	}
	if params.Reader == nil || params.Size <= 0 {
		return fmt.Errorf("storage: multipart part %d payload is invalid: %w", params.PartNumber, ErrInvalidObject)
	}
	return nil
}

// s3EndpointURL 补齐 S3 endpoint 的 URL scheme
func s3EndpointURL(cfg *Config) string {
	if strings.HasPrefix(cfg.Endpoint, "http://") || strings.HasPrefix(cfg.Endpoint, "https://") {
		return cfg.Endpoint
	}
	if cfg.UseSSL {
		return "https://" + cfg.Endpoint
	}
	return "http://" + cfg.Endpoint
}

// s3BucketNotFound 判断 bucket 是否不存在
func s3BucketNotFound(err error) bool {
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	errorCode := apiError.ErrorCode()
	return errorCode == "NotFound" || errorCode == "NoSuchBucket" || errorCode == "404"
}

// startStorageSpan 创建对象存储操作子 span 并写入公共属性
func (fsc *S3Client) startStorageSpan(ctx context.Context, operation string, objectName string) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	provider := string(fsc.provider)
	if provider == "" {
		provider = "s3"
	}
	ctx, span := apctracing.Start(ctx, fmt.Sprintf("storage.%s.%s", provider, operation))
	span.SetAttributes(
		attribute.String("storage.system", provider),
		attribute.String("storage.bucket", fsc.bucketName),
		attribute.String("storage.endpoint", fsc.endpoint),
	)
	if objectName != "" {
		span.SetAttributes(attribute.String("storage.object.name", objectName))
	}
	return ctx, span
}
