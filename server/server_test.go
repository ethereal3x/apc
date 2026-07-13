package server

import (
	"context"
	"testing"
	"time"
)

// TestRunServer 验证服务随 context 超时优雅退出
func TestRunServer(t *testing.T) {
	grpcServer := NewRpcServer()
	httpServer := NewHttpServer()
	grpcServer.SetAddress("127.0.0.1:0")
	httpServer.SetAddress("127.0.0.1:0")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := RunGrpcGatewayServiceContext(ctx, grpcServer, httpServer)
	// 超时取消后服务优雅退出，不应返回错误
	if err != nil {
		t.Fatalf("RunGrpcGatewayService() error = %v", err)
	}
}

// TestRunGrpcGatewayServiceNilServers 验证传入 nil 服务时不 panic
func TestRunGrpcGatewayServiceNilServers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunGrpcGatewayServiceContext(ctx, nil, nil)
	if err != nil {
		t.Fatalf("RunGrpcGatewayService() with nil servers error = %v", err)
	}
}
