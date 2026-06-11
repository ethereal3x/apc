package server

import (
	"context"
	"log"
	"net"
	"sync"

	"github.com/ethereal3x/apc/config"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

type GrpcServer struct {
	Server
	// 流式处理 拦截器
	streamInterceptors []grpc.StreamServerInterceptor
	// rpc 拦截器
	unaryInterceptors []grpc.UnaryServerInterceptor
	// 注册proto service
	registerFunction func(*grpc.Server)
}

func (s *GrpcServer) SetInterceptors(streamInterceptors []grpc.StreamServerInterceptor, unaryInterceptors []grpc.UnaryServerInterceptor) {
	s.streamInterceptors = append(s.streamInterceptors, streamInterceptors...)
	s.unaryInterceptors = append(s.unaryInterceptors, unaryInterceptors...)
}

// SetRegisterFunc 设置 gRPC 服务注册回调函数
func (s *GrpcServer) SetRegisterFunc(fn func(*grpc.Server)) {
	s.registerFunction = fn
}

func (s *GrpcServer) genServerOptions() ([]grpc.StreamServerInterceptor, []grpc.UnaryServerInterceptor) {
	streamInterceptors := []grpc.StreamServerInterceptor{
		grpc_ctxtags.StreamServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
		grpc_recovery.StreamServerInterceptor(grpc_recovery.WithRecoveryHandler(goRoutineStack)),
	}
	streamInterceptors = append(streamInterceptors, s.streamInterceptors...)

	unaryInterceptors := []grpc.UnaryServerInterceptor{
		grpc_ctxtags.UnaryServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
		grpc_recovery.UnaryServerInterceptor(grpc_recovery.WithRecoveryHandler(goRoutineStack)),
	}
	unaryInterceptors = append(unaryInterceptors, s.unaryInterceptors...)
	return streamInterceptors, unaryInterceptors
}

func NewRpcServer() *GrpcServer {
	return &GrpcServer{
		Server:             NewServer(config.GetConf().Server.GrpcAddr),
		streamInterceptors: []grpc.StreamServerInterceptor{},
		unaryInterceptors:  []grpc.UnaryServerInterceptor{},
	}
}

// run 启动 gRPC 服务并监听 ctx 取消信号执行优雅关闭
func (s *GrpcServer) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	sock, err := net.Listen("tcp", s.address)
	if err != nil {
		log.Fatalf("rpc start to listen error: %v", err)
	}
	streamServerInterceptors, unaryServerInterceptors := s.genServerOptions()
	serverOptions := []grpc.ServerOption{
		grpc.ChainStreamInterceptor(streamServerInterceptors...),
		grpc.ChainUnaryInterceptor(unaryServerInterceptors...),
	}

	server := grpc.NewServer(serverOptions...)
	if s.registerFunction != nil {
		s.registerFunction(server)
	}

	go func() {
		<-ctx.Done()
		s.log.ContextInfo(ctx, "rpc service quit")
		server.GracefulStop()
	}()

	_ = server.Serve(sock)
}

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
