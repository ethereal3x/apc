package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/ethereal3x/apc/logger"
	apctracing "github.com/ethereal3x/apc/tracing"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

type Server struct {
	address string
	log     logger.Logger
}

// SetAddress 设置服务监听地址
func (server *Server) SetAddress(address string) {
	server.address = address
}

// SetLogger 设置自定义 logger 实例
func (server *Server) SetLogger(log logger.Logger) {
	server.log = log
}

// NewServer 创建基础服务实例
func NewServer(addr string) Server {
	return Server{
		log:     logger.NewLogger(&config.GetConf().Plugin.Log),
		address: addr,
	}
}

// generateFrameworkMetadata 生成 grpc-gateway 透传到 gRPC metadata 的框架信息
func generateFrameworkMetadata() runtime.ServeMuxOption {
	return runtime.WithMetadata(func(ctx context.Context, request *http.Request) metadata.MD {
		md, ok := runtime.ServerMetadataFromContext(ctx)
		if !ok {
			md = runtime.ServerMetadata{
				HeaderMD:  metadata.New(map[string]string{}),
				TrailerMD: metadata.New(map[string]string{}),
			}
		}
		if method, ok := runtime.RPCMethod(ctx); ok {
			md.HeaderMD.Append("RPC-Method", method)
		}
		if pattern, ok := runtime.HTTPPathPattern(ctx); ok {
			md.HeaderMD.Append("HTTP-Pattern", pattern)
		}
		// 设置X-Real-Ip
		md.HeaderMD.Append("X-Real-Ip", request.Header.Get("X-Forwarded-For"))
		// 透传X-Forwarded-Method X-Forwarded-Uri
		md.HeaderMD.Append("X-Forwarded-Method", request.Header.Get("X-Forwarded-Method"))
		md.HeaderMD.Append("X-Forwarded-Uri", request.Header.Get("X-Forwarded-Uri"))
		md.HeaderMD.Append("P-User-Host", request.Header.Get("P-User-Host"))
		return md.HeaderMD
	})
}

// propagateTracingMetadata 透传 OpenTelemetry tracing metadata
func propagateTracingMetadata() runtime.ServeMuxOption {
	return runtime.WithMetadata(func(ctx context.Context, _ *http.Request) metadata.MD {
		md, ok := runtime.ServerMetadataFromContext(ctx)
		if !ok {
			md = runtime.ServerMetadata{
				HeaderMD:  metadata.New(map[string]string{}),
				TrailerMD: metadata.New(map[string]string{}),
			}
		}
		if !oteltrace.SpanContextFromContext(ctx).IsValid() {
			return md.HeaderMD
		}
		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(ctx, carrier)
		return metadata.Join(md.HeaderMD, metadata.New(carrier))
	})
}

// propagateTracing 创建并透传 HTTP gateway 入口 tracing span
func propagateTracing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		ctx, span := apctracing.Start(ctx, fmt.Sprintf("HTTP-gRPC %s %s", request.Method, request.URL.Path))
		defer span.End()
		responseWriter.Header().Set("Tracing-Id", apctracing.TraceID(ctx))
		next.ServeHTTP(responseWriter, request.WithContext(ctx))
	})
}

// Runner 是可被 Supervisor 编排的服务，Run 在 ctx 取消前阻塞，返回服务运行期间的错误
type Runner interface {
	Run(ctx context.Context) error
}

// RunGrpcGatewayService 启动 gRPC 和 HTTP gateway 服务，监听系统信号并在服务错误时退出
func RunGrpcGatewayService(grpcServer *GrpcServer, httpServer *HttpServer) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := RunGrpcGatewayServiceContext(ctx, grpcServer, httpServer); err != nil {
		log.Fatalf("run grpc gateway service failed: %v", err)
	}
}

// RunGrpcGatewayServiceContext 启动 gRPC 和 HTTP gateway 服务，任一服务出错时取消其他服务
// 信号监听由调用方负责，ctx 取消后所有服务执行优雅关闭
func RunGrpcGatewayServiceContext(ctx context.Context, grpcServer *GrpcServer, httpServer *HttpServer) error {
	shutdownTracing, err := initTracingProvider()
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if shutdownErr := shutdownTracing(shutdownCtx); shutdownErr != nil {
			log.Printf("shutdown tracing provider failed: %v", shutdownErr)
		}
	}()
	if err := WritePidFile(); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer RmPidFile()

	// 子 context 取消时不会中断父 ctx，使 shutdown 能正常执行
	supervisorCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var runners []Runner
	if grpcServer != nil && grpcServer.address != "" {
		runners = append(runners, grpcServer)
	}
	if httpServer != nil && httpServer.address != "" {
		runners = append(runners, httpServer)
	}
	if len(runners) == 0 {
		return nil
	}

	errCh := runServices(supervisorCtx, runners)

	// 任一服务返回后取消其他服务并继续等待退出
	var firstErr error
	for range runners {
		if serviceErr := <-errCh; serviceErr != nil && firstErr == nil {
			firstErr = serviceErr
		}
		cancel()
	}
	return firstErr
}

// runServices 并行启动所有服务，通过 fan-in channel 汇聚各服务错误
func runServices(ctx context.Context, runners []Runner) <-chan error {
	errCh := make(chan error, len(runners))
	for _, runner := range runners {
		go func(service Runner) {
			errCh <- service.Run(ctx)
		}(runner)
	}
	return errCh
}

// initTracingProvider 根据配置初始化 tracing provider，返回关闭函数和初始化错误
func initTracingProvider() (func(context.Context) error, error) {
	shutdown, err := apctracing.InitProvider(config.GetConf().Plugin.Tracing)
	if err != nil {
		return nil, fmt.Errorf("init tracing provider: %w", err)
	}
	return shutdown, nil
}
