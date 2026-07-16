package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/ethereal3x/apc/config"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

const defaultGrpcStopTimeout = 5 * time.Second

type GrpcServer struct {
	Server
	// 流式处理 拦截器
	streamInterceptors []grpc.StreamServerInterceptor
	// rpc 拦截器
	unaryInterceptors []grpc.UnaryServerInterceptor
	// 注册proto service
	registerFunction func(*grpc.Server)
	// panic 恢复策略
	recoveryHandler GRPCRecoveryHandler
}

// SetInterceptors 设置 gRPC 流式和 unary 拦截器
func (server *GrpcServer) SetInterceptors(streamInterceptors []grpc.StreamServerInterceptor, unaryInterceptors []grpc.UnaryServerInterceptor) {
	server.streamInterceptors = append(server.streamInterceptors, streamInterceptors...)
	server.unaryInterceptors = append(server.unaryInterceptors, unaryInterceptors...)
}

// SetRegisterFunc 设置 gRPC 服务注册回调函数
func (server *GrpcServer) SetRegisterFunc(fn func(*grpc.Server)) {
	server.registerFunction = fn
}

// SetRecoveryHandler 设置自定义 gRPC panic 错误映射策略
func (server *GrpcServer) SetRecoveryHandler(handler GRPCRecoveryHandler) {
	server.recoveryHandler = handler
}

// genServerOptions 生成 gRPC 服务端拦截器配置
func (server *GrpcServer) genServerOptions() ([]grpc.StreamServerInterceptor, []grpc.UnaryServerInterceptor) {
	streamInterceptors := []grpc.StreamServerInterceptor{
		grpc_ctxtags.StreamServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
		grpc_recovery.StreamServerInterceptor(grpc_recovery.WithRecoveryHandlerContext(server.grpcRecoveryHandler)),
	}
	streamInterceptors = append(streamInterceptors, server.streamInterceptors...)

	unaryInterceptors := []grpc.UnaryServerInterceptor{
		grpc_ctxtags.UnaryServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
		grpc_recovery.UnaryServerInterceptor(grpc_recovery.WithRecoveryHandlerContext(server.grpcRecoveryHandler)),
	}
	unaryInterceptors = append(unaryInterceptors, server.unaryInterceptors...)
	return streamInterceptors, unaryInterceptors
}

// NewRpcServer 创建 gRPC 服务实例
func NewRpcServer() *GrpcServer {
	return &GrpcServer{
		Server:             NewServer(config.GetConf().Server.GrpcAddr),
		streamInterceptors: []grpc.StreamServerInterceptor{},
		unaryInterceptors:  []grpc.UnaryServerInterceptor{},
	}
}

// Run 启动 gRPC 服务并监听 ctx 取消信号执行优雅关闭，返回服务运行期间的错误
func (server *GrpcServer) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.address)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", server.address, err)
	}
	streamServerInterceptors, unaryServerInterceptors := server.genServerOptions()
	serverOptions := []grpc.ServerOption{
		grpc.ChainStreamInterceptor(streamServerInterceptors...),
		grpc.ChainUnaryInterceptor(unaryServerInterceptors...),
	}

	grpcServer := grpc.NewServer(serverOptions...)
	if server.registerFunction != nil {
		server.registerFunction(grpcServer)
	}

	go server.shutdownGrpcServer(ctx, grpcServer)

	if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

// shutdownGrpcServer 优雅停止 gRPC 服务，超时后强制停止
func (server *GrpcServer) shutdownGrpcServer(ctx context.Context, grpcServer *grpc.Server) {
	<-ctx.Done()
	server.log.ContextInfo(ctx, "rpc service quit")
	stoppedCh := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stoppedCh)
	}()

	timer := time.NewTimer(defaultGrpcStopTimeout)
	defer timer.Stop()
	select {
	case <-stoppedCh:
	case <-timer.C:
		grpcServer.Stop()
	}
}

// setMarshalerOption 设置 grpc-gateway JSON 序列化选项
func setMarshalerOption() runtime.ServeMuxOption {
	marshaler := &runtime.HTTPBodyMarshaler{
		Marshaler: &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:     true,
				EmitDefaultValues: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		},
	}
	return runtime.WithMarshalerOption(runtime.MIMEWildcard, marshaler)
}
