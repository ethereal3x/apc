//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereal3x/apc/storage"
)

const (
	STORAGE_PROVIDER_MINIO  = string(storage.STORAGE_PROVIDER_MINIO)
	STORAGE_PROVIDER_RUSTFS = string(storage.STORAGE_PROVIDER_RUSTFS)

	defaultListenAddr   = "127.0.0.1:18080"
	defaultIndexHTML    = "storage/integrationserver/index.html"
	defaultObjectPrefix = "integration/multipart"
	presignedGetExpiry  = 24 * time.Hour
)

type uploadSession struct {
	Provider     string
	ObjectName   string
	UploadID     string
	TotalSize    int64
	ContentType  string
	UploadedSize int64
	Parts        []storage.UploadedPart
	CancelChan   chan struct{}
	Canceled     bool
}

type initRequest struct {
	FileName    string `json:"file_name"`
	TotalSize   int64  `json:"total_size"`
	ContentType string `json:"content_type"`
	Provider    string `json:"provider"`
}

type apiResponse struct {
	OK           bool                             `json:"ok"`
	Error        string                           `json:"error,omitempty"`
	Default      string                           `json:"default,omitempty"`
	Provider     string                           `json:"provider,omitempty"`
	ObjectName   string                           `json:"object_name,omitempty"`
	UploadID     string                           `json:"upload_id,omitempty"`
	Part         *storage.UploadedPart            `json:"part,omitempty"`
	Progress     *storage.MultipartUploadProgress `json:"progress,omitempty"`
	Result       *storage.UploadResult            `json:"result,omitempty"`
	PublicURL    string                           `json:"public_url,omitempty"`
	PresignedURL string                           `json:"presigned_url,omitempty"`
}

type multipartServer struct {
	objectPrefix   string
	storageMu      sync.Mutex
	storageClients map[string]storage.ObjectStorage
	sessionMu      sync.Mutex
	sessions       map[string]*uploadSession
}

// main 启动对象存储分片上传集成测试服务
func main() {
	server := &multipartServer{
		objectPrefix:   envString("APC_MULTIPART_OBJECT_PREFIX", defaultObjectPrefix),
		storageClients: make(map[string]storage.ObjectStorage),
		sessions:       make(map[string]*uploadSession),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handleIndex)
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/api/config", server.handleConfig)
	mux.HandleFunc("/api/init", server.handleInit)
	mux.HandleFunc("/api/part", server.handlePart)
	mux.HandleFunc("/api/progress", server.handleProgress)
	mux.HandleFunc("/api/complete", server.handleComplete)
	mux.HandleFunc("/api/cancel", server.handleCancel)

	listenAddr := envString("APC_MULTIPART_LISTEN_ADDR", defaultListenAddr)
	log.Printf("storage multipart integration server listening on http://%s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

// handleIndex 返回分片上传测试页面
func (server *multipartServer) handleIndex(responseWriter http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(responseWriter, request)
		return
	}
	indexHTMLPath := envString("APC_MULTIPART_HTML_PATH", defaultIndexHTML)
	if _, err := os.Stat(indexHTMLPath); err != nil {
		if _, fallbackErr := os.Stat("index.html"); fallbackErr != nil {
			writeJSON(responseWriter, http.StatusNotFound, apiResponse{Error: fmt.Sprintf("index html not found: %v", err)})
			return
		}
		indexHTMLPath = "index.html"
	}
	http.ServeFile(responseWriter, request, indexHTMLPath)
}

// handleHealthz 返回服务健康状态
func (server *multipartServer) handleHealthz(responseWriter http.ResponseWriter, request *http.Request) {
	writeJSON(responseWriter, http.StatusOK, apiResponse{OK: true})
}

// handleConfig 返回测试页面默认配置
func (server *multipartServer) handleConfig(responseWriter http.ResponseWriter, request *http.Request) {
	provider, err := normalizeStorageProvider("")
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	writeJSON(responseWriter, http.StatusOK, apiResponse{OK: true, Default: provider})
}

// handleInit 初始化一个分片上传会话
func (server *multipartServer) handleInit(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeJSON(responseWriter, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var initParams initRequest
	if err := json.NewDecoder(request.Body).Decode(&initParams); err != nil {
		writeJSON(responseWriter, http.StatusBadRequest, apiResponse{Error: fmt.Sprintf("decode init request: %v", err)})
		return
	}
	if initParams.FileName == "" || initParams.TotalSize <= 0 {
		writeJSON(responseWriter, http.StatusBadRequest, apiResponse{Error: "file_name and total_size are required"})
		return
	}
	provider, err := normalizeStorageProvider(initParams.Provider)
	if err != nil {
		writeJSON(responseWriter, http.StatusBadRequest, apiResponse{Error: err.Error()})
		return
	}
	storageClient, err := server.storageClient(request.Context(), provider)
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	objectName := server.multipartObjectName(provider, initParams.FileName)
	upload, err := storageClient.InitiateMultipartUpload(request.Context(), storage.InitiateMultipartUploadParams{
		ObjectName: objectName,
		Options:    storage.UploadOptions{ContentType: initParams.ContentType},
	})
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	server.sessionMu.Lock()
	server.sessions[upload.UploadID] = &uploadSession{
		Provider:    provider,
		ObjectName:  upload.ObjectName,
		UploadID:    upload.UploadID,
		TotalSize:   initParams.TotalSize,
		ContentType: initParams.ContentType,
		CancelChan:  make(chan struct{}),
	}
	server.sessionMu.Unlock()
	writeJSON(responseWriter, http.StatusOK, apiResponse{
		OK:         true,
		Provider:   provider,
		ObjectName: upload.ObjectName,
		UploadID:   upload.UploadID,
	})
}

// handlePart 上传一个分片并更新内存会话进度
func (server *multipartServer) handlePart(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeJSON(responseWriter, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	partNumber, err := strconv.Atoi(request.URL.Query().Get("part_number"))
	if err != nil {
		writeJSON(responseWriter, http.StatusBadRequest, apiResponse{Error: "invalid part_number"})
		return
	}
	session := server.getSession(request.URL.Query().Get("upload_id"))
	if session == nil {
		writeJSON(responseWriter, http.StatusNotFound, apiResponse{Error: "upload session not found"})
		return
	}
	storageClient, err := server.storageClient(request.Context(), session.Provider)
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	progressChan := make(chan storage.MultipartUploadProgress, 1)
	server.sessionMu.Lock()
	uploadedSizeBefore := session.UploadedSize
	cancelChan := session.CancelChan
	server.sessionMu.Unlock()

	part, err := storageClient.UploadPart(request.Context(), storage.UploadPartParams{
		ObjectName:         session.ObjectName,
		UploadID:           session.UploadID,
		PartNumber:         partNumber,
		Reader:             request.Body,
		Size:               request.ContentLength,
		TotalSize:          session.TotalSize,
		UploadedSizeBefore: uploadedSizeBefore,
		ProgressChan:       progressChan,
		CancelChan:         cancelChan,
	})
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	progress := server.recordUploadedPart(session.UploadID, part, progressChan)
	writeJSON(responseWriter, http.StatusOK, apiResponse{OK: true, Provider: session.Provider, Part: &part, Progress: &progress})
}

// handleProgress 查询对象存储服务端当前已接收的分片进度
func (server *multipartServer) handleProgress(responseWriter http.ResponseWriter, request *http.Request) {
	session := server.getSession(request.URL.Query().Get("upload_id"))
	if session == nil {
		writeJSON(responseWriter, http.StatusNotFound, apiResponse{Error: "upload session not found"})
		return
	}
	storageClient, err := server.storageClient(request.Context(), session.Provider)
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	progress, err := storageClient.GetMultipartUploadProgress(request.Context(), storage.MultipartUploadProgressParams{
		ObjectName: session.ObjectName,
		UploadID:   session.UploadID,
		TotalSize:  session.TotalSize,
	})
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	writeJSON(responseWriter, http.StatusOK, apiResponse{OK: true, Provider: session.Provider, Progress: &progress})
}

// handleComplete 合并当前会话已上传的所有分片
func (server *multipartServer) handleComplete(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeJSON(responseWriter, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	session := server.getSession(request.URL.Query().Get("upload_id"))
	if session == nil {
		writeJSON(responseWriter, http.StatusNotFound, apiResponse{Error: "upload session not found"})
		return
	}
	storageClient, err := server.storageClient(request.Context(), session.Provider)
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	result, err := storageClient.CompleteMultipartUpload(request.Context(), storage.CompleteMultipartUploadParams{
		ObjectName: session.ObjectName,
		UploadID:   session.UploadID,
		Parts:      session.Parts,
		Options:    storage.UploadOptions{ContentType: session.ContentType},
	})
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	presignedURL, err := storageClient.PresignedGetURL(request.Context(), session.ObjectName, presignedGetExpiry)
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	server.deleteSession(session.UploadID)
	writeJSON(responseWriter, http.StatusOK, apiResponse{
		OK:           true,
		Provider:     session.Provider,
		ObjectName:   session.ObjectName,
		Result:       &result,
		PublicURL:    storageClient.PublicURL(session.ObjectName),
		PresignedURL: presignedURL,
	})
}

// handleCancel 取消上传会话并通知对象存储清理已上传分片
func (server *multipartServer) handleCancel(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeJSON(responseWriter, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	session := server.cancelSession(request.URL.Query().Get("upload_id"))
	if session == nil {
		writeJSON(responseWriter, http.StatusNotFound, apiResponse{Error: "upload session not found"})
		return
	}
	storageClient, err := server.storageClient(context.Background(), session.Provider)
	if err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	if err := storageClient.AbortMultipartUpload(context.Background(), storage.AbortMultipartUploadParams{
		ObjectName: session.ObjectName,
		UploadID:   session.UploadID,
	}); err != nil {
		writeJSON(responseWriter, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}
	server.deleteSession(session.UploadID)
	writeJSON(responseWriter, http.StatusOK, apiResponse{OK: true, Provider: session.Provider})
}

// multipartObjectName 构造集成测试上传对象路径
func (server *multipartServer) multipartObjectName(provider string, fileName string) string {
	baseName := filepath.Base(fileName)
	objectPrefix := strings.Trim(server.objectPrefix, "/")
	return fmt.Sprintf("%s/%s/%d-%s", objectPrefix, provider, time.Now().UnixNano(), baseName)
}

// storageClient 获取指定 provider 的对象存储客户端
func (server *multipartServer) storageClient(ctx context.Context, provider string) (storage.ObjectStorage, error) {
	server.storageMu.Lock()
	client := server.storageClients[provider]
	server.storageMu.Unlock()
	if client != nil {
		return client, nil
	}
	client, err := newStorageClient(ctx, provider)
	if err != nil {
		return nil, err
	}
	server.storageMu.Lock()
	if cachedClient := server.storageClients[provider]; cachedClient != nil {
		server.storageMu.Unlock()
		return cachedClient, nil
	}
	server.storageClients[provider] = client
	server.storageMu.Unlock()
	return client, nil
}

// getSession 读取当前上传会话
func (server *multipartServer) getSession(uploadID string) *uploadSession {
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	return server.sessions[uploadID]
}

// recordUploadedPart 记录分片上传结果并返回最新进度
func (server *multipartServer) recordUploadedPart(uploadID string, part storage.UploadedPart, progressChan <-chan storage.MultipartUploadProgress) storage.MultipartUploadProgress {
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	session := server.sessions[uploadID]
	if session == nil {
		return storage.MultipartUploadProgress{}
	}
	session.Parts = append(session.Parts, part)
	session.UploadedSize += part.Size
	select {
	case progress := <-progressChan:
		return progress
	default:
		return storage.MultipartUploadProgress{
			ObjectName:   session.ObjectName,
			UploadID:     session.UploadID,
			UploadedSize: session.UploadedSize,
			TotalSize:    session.TotalSize,
			PartNumber:   part.PartNumber,
			PartSize:     part.Size,
			Completed:    session.UploadedSize >= session.TotalSize,
			Percent:      uploadPercent(session.UploadedSize, session.TotalSize),
		}
	}
}

// cancelSession 标记上传会话取消并关闭取消信号
func (server *multipartServer) cancelSession(uploadID string) *uploadSession {
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	session := server.sessions[uploadID]
	if session == nil {
		return nil
	}
	if !session.Canceled {
		close(session.CancelChan)
		session.Canceled = true
	}
	return session
}

// deleteSession 删除内存中的上传会话
func (server *multipartServer) deleteSession(uploadID string) {
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	delete(server.sessions, uploadID)
}

// newStorageClient 按 provider 创建对象存储客户端
func newStorageClient(ctx context.Context, provider string) (storage.ObjectStorage, error) {
	cfg, err := storageConfigFromEnv(provider)
	if err != nil {
		return nil, err
	}
	client, err := storage.NewS3Client(ctx, storage.NewS3ClientParams{
		Provider: storage.StorageProvider(provider),
		Config:   cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("init %s storage client: %w", provider, err)
	}
	return client, nil
}

// storageConfigFromEnv 按 provider 读取对象存储配置
func storageConfigFromEnv(provider string) (*storage.Config, error) {
	switch provider {
	case STORAGE_PROVIDER_MINIO:
		return minioConfigFromEnv(), nil
	case STORAGE_PROVIDER_RUSTFS:
		return rustFSConfigFromEnv(), nil
	default:
		return nil, fmt.Errorf("unsupported storage provider %q", provider)
	}
}

// minioConfigFromEnv 从环境变量读取 MinIO 配置
func minioConfigFromEnv() *storage.Config {
	return &storage.Config{
		Endpoint:   os.Getenv("APC_MINIO_ENDPOINT"),
		AccessKey:  os.Getenv("APC_MINIO_ACCESS_KEY"),
		SecretKey:  os.Getenv("APC_MINIO_SECRET_KEY"),
		BucketName: envString("APC_MINIO_BUCKET", os.Getenv("APC_MINIO_BUCKET_NAME")),
		UseSSL:     envBool("APC_MINIO_USE_SSL"),
		Region:     os.Getenv("APC_MINIO_REGION"),
	}
}

// rustFSConfigFromEnv 从环境变量读取 RustFS 配置
func rustFSConfigFromEnv() *storage.Config {
	return &storage.Config{
		Endpoint:   os.Getenv("APC_RUSTFS_ENDPOINT"),
		AccessKey:  os.Getenv("APC_RUSTFS_ACCESS_KEY"),
		SecretKey:  os.Getenv("APC_RUSTFS_SECRET_KEY"),
		BucketName: envString("APC_RUSTFS_BUCKET", os.Getenv("APC_RUSTFS_BUCKET_NAME")),
		UseSSL:     envBool("APC_RUSTFS_USE_SSL"),
		Region:     os.Getenv("APC_RUSTFS_REGION"),
	}
}

// normalizeStorageProvider 规范化页面传入的对象存储 provider
func normalizeStorageProvider(provider string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(provider))
	if value == "" {
		value = envString("APC_STORAGE_PROVIDER", STORAGE_PROVIDER_MINIO)
	}
	switch value {
	case STORAGE_PROVIDER_MINIO, STORAGE_PROVIDER_RUSTFS:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported storage provider %q", provider)
	}
}

// envString 读取字符串环境变量并处理默认值
func envString(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// envBool 读取布尔环境变量
func envBool(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

// uploadPercent 计算上传百分比
func uploadPercent(uploadedSize int64, totalSize int64) float64 {
	if totalSize <= 0 {
		return 0
	}
	return float64(uploadedSize) * 100 / float64(totalSize)
}

// writeJSON 写入 JSON 响应
func writeJSON(responseWriter http.ResponseWriter, statusCode int, response apiResponse) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	if err := json.NewEncoder(responseWriter).Encode(response); err != nil {
		log.Printf("write json response: %v", err)
	}
}
