package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
)

const defaultWriteTimeout = 15 * time.Second

type HttpServer struct {
	Server
	serveMuxOptions  []runtime.ServeMuxOption
	registerFunction func(context.Context, *runtime.ServeMux) error
	writeTimeout     time.Duration
	middlewares      []func(http.Handler) http.Handler
}

func (s *HttpServer) SetRegisterFunc(registerFunc func(context.Context, *runtime.ServeMux) error) {
	s.registerFunction = registerFunc
}

func (s *HttpServer) SetServeMuxOpts(opts []runtime.ServeMuxOption) {
	s.serveMuxOptions = opts
}

// SetWriteTimeout 设置 HTTP 写超时，0 表示不限制（用于流式响应）
func (s *HttpServer) SetWriteTimeout(d time.Duration) {
	s.writeTimeout = d
}

// SetMiddleware 设置 HTTP 中间件链，按顺序包裹在 grpc-gateway mux 外层。
// 中间件在 recovery/CORS/tracing 之后、mux 之前执行。
func (s *HttpServer) SetMiddleware(mws ...func(http.Handler) http.Handler) {
	s.middlewares = append(s.middlewares, mws...)
}

func NewHttpServer() *HttpServer {
	return &HttpServer{
		Server:          NewServer(config.GetConf().Server.GatewayAddr),
		serveMuxOptions: []runtime.ServeMuxOption{},
		writeTimeout:    defaultWriteTimeout,
	}
}

// run 初始化 HTTP gateway mux 并注册服务路由，监听 ctx 取消信号优雅关闭
func (s *HttpServer) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	mux := runtime.NewServeMux(
		append(s.serveMuxOptions, propagateTracingMetadata(), generateFrameworkMetadata(), setMarshalerOption())...)
	if s.registerFunction != nil {
		if err := s.registerFunction(ctx, mux); err != nil {
			s.log.Fatal("register function failed", zap.Error(err))
		}
	}

	handler := http.Handler(mux)
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}

	hs := &http.Server{
		Addr:              s.address,
		Handler:           recovery(allowCORS(propagateTracing(handler))),
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      s.writeTimeout,
	}

	go func() {
		<-ctx.Done()
		s.log.Info("gateway.Shutdown now")
		_ = hs.Shutdown(context.Background())
	}()

	if err := hs.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		s.log.Fatal("gateway.http.server.failed", zap.Error(err))
	}
}
