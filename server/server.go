package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	os_runtime "runtime"

	"github.com/ethereal3x/apc/config"
	"github.com/ethereal3x/apc/logger"
	apctracing "github.com/ethereal3x/apc/tracing"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	serviceWaitGroup = sync.WaitGroup{}
	hsServiceQuit    = make(chan int)
	rsServiceQuit    = make(chan int)
)

type Server struct {
	address string
	log     logger.Logger
}

func (s *Server) SetAddress(address string) {
	s.address = address
}

// SetLogger 设置自定义 logger 实例
func (s *Server) SetLogger(log logger.Logger) {
	s.log = log
}

// NewServer 创建基础服务实例
func NewServer(addr string) Server {
	return Server{
		log:     logger.NewLogger(&config.GetConf().Plugin.Log),
		address: addr,
	}
}

func goRoutineStack(p interface{}) (err error) {
	var buf [8192]byte
	n := os_runtime.Stack(buf[:], false)
	stack := strings.Split(string(buf[:n]), "\n")
	var stackNew strings.Builder
	for i := 0; i < len(stack)-1; i++ {
		line := stack[i]
		if strings.Contains(line, "go-grpc-middleware") || strings.Contains(line, "google.golang.org") {
			continue
		}
		stackNew.WriteString(line + "\n")
	}
	fmt.Printf("panic: %v, stack: %s\n", p, &stackNew)
	return status.Errorf(codes.Unknown, "panic triggered: %v", p)
}

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

func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			err := recover()
			if err != nil {
				_ = goRoutineStack(err)
				jsonBody, _ := json.Marshal(map[string]interface{}{
					"error": fmt.Sprintf("internal server panic: %v", err),
				})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write(jsonBody)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// allowCORS 处理跨域请求头和预检请求
func allowCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				headers := []string{"Content-Type", "Accept", "Authorization"}
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ","))
				methods := []string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS"}
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ","))
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// propagateTracing 创建并透传 HTTP gateway 入口 tracing span
func propagateTracing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := apctracing.Start(ctx, fmt.Sprintf("HTTP-gRPC %s %s", r.Method, r.URL.Path))
		defer span.End()
		w.Header().Set("Tracing-Id", apctracing.TraceID(ctx))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RunGrpcGatewayService 启动 gRPC 和 HTTP gateway 服务
func RunGrpcGatewayService(rs *GrpcServer, hs *HttpServer) {
	shutdownTracing := initTracingProvider()
	defer func() {
		if err := shutdownTracing(context.Background()); err != nil {
			log.Printf("shutdown tracing provider failed: %v", err)
		}
	}()
	if err := WritePidFile(); err != nil {
		log.Fatalf("failed to write pid file: %v", err)
		return
	}
	defer RmPidFile()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sCnt = 0
	if rs.address != "" {
		sCnt++
		go rs.run(ctx)
	}
	if hs.address != "" {
		sCnt++
		go hs.run(ctx)
	}

	serviceWaitGroup.Add(sCnt)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		s := <-sigCh
		hsServiceQuit <- 1
		rsServiceQuit <- 1
		log.Printf("ready.to.shutdown: %v", s.String())
	}()

	serviceWaitGroup.Wait()
}

// initTracingProvider 根据配置初始化 tracing provider
func initTracingProvider() func(context.Context) error {
	shutdown, err := apctracing.InitProvider(config.GetConf().Plugin.Tracing)
	if err != nil {
		log.Fatalf("init tracing provider failed: %v", err)
	}
	return shutdown
}
