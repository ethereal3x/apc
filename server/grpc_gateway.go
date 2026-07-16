package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

const (
	defaultWriteTimeout        = 15 * time.Second
	defaultHTTPShutdownTimeout = 5 * time.Second
)

type HttpServer struct {
	Server
	serveMuxOptions    []runtime.ServeMuxOption
	registerFunction   func(context.Context, *runtime.ServeMux) error
	writeTimeout       time.Duration
	middlewares        []func(http.Handler) http.Handler
	corsAllowedHeaders []string
	corsPolicy         *corsPolicy
	recoveryHandler    HTTPRecoveryHandler
}

// SetRegisterFunc 设置 HTTP gateway 路由注册回调函数
func (server *HttpServer) SetRegisterFunc(registerFunc func(context.Context, *runtime.ServeMux) error) {
	server.registerFunction = registerFunc
}

// SetServeMuxOpts 设置 grpc-gateway mux 选项
func (server *HttpServer) SetServeMuxOpts(opts []runtime.ServeMuxOption) {
	server.serveMuxOptions = opts
}

// SetWriteTimeout 设置 HTTP 写超时，0 表示不限制（用于流式响应）
func (server *HttpServer) SetWriteTimeout(timeout time.Duration) {
	server.writeTimeout = timeout
}

// SetMiddleware 设置 HTTP 中间件链，按顺序包裹在 CORS 和 grpc-gateway mux 外层
func (server *HttpServer) SetMiddleware(middlewares ...func(http.Handler) http.Handler) {
	server.middlewares = append(server.middlewares, middlewares...)
}

// SetCORSAllowedHeaders 设置 CORS 允许的自定义请求头，覆盖默认的 Content-Type/Accept/Authorization
//
// Deprecated: 使用 SetCORSConfig 配置完整且可关闭的 CORS 策略
func (server *HttpServer) SetCORSAllowedHeaders(headers []string) {
	server.corsAllowedHeaders = append([]string(nil), headers...)
	server.corsPolicy = nil
}

// SetCORSConfig 校验并设置完整 CORS 策略，Enabled=false 时不处理任何跨域请求
func (server *HttpServer) SetCORSConfig(config CORSConfig) error {
	policy, err := newCORSPolicy(config)
	if err != nil {
		return err
	}
	server.corsPolicy = policy
	return nil
}

// SetRecoveryHandler 设置自定义 HTTP panic 响应处理策略
func (server *HttpServer) SetRecoveryHandler(handler HTTPRecoveryHandler) {
	server.recoveryHandler = handler
}

// NewHttpServer 创建 HTTP gateway 服务实例
func NewHttpServer() *HttpServer {
	return &HttpServer{
		Server:          NewServer(config.GetConf().Server.GatewayAddr),
		serveMuxOptions: []runtime.ServeMuxOption{},
		writeTimeout:    defaultWriteTimeout,
	}
}

// Run 初始化 HTTP gateway mux 并注册服务路由，监听 ctx 取消信号优雅关闭，返回服务运行期间的错误
func (server *HttpServer) Run(ctx context.Context) error {
	mux := runtime.NewServeMux(
		append(server.serveMuxOptions, propagateTracingMetadata(), generateFrameworkMetadata(), setMarshalerOption())...)
	if server.registerFunction != nil {
		if err := server.registerFunction(ctx, mux); err != nil {
			return fmt.Errorf("register gateway route: %w", err)
		}
	}

	httpServer := &http.Server{
		Addr:              server.address,
		Handler:           server.buildHandler(mux),
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      server.writeTimeout,
	}
	shutdownErrCh := make(chan error, 1)
	go server.shutdownHTTPServer(ctx, httpServer, shutdownErrCh)

	if err := httpServer.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			select {
			case shutdownErr := <-shutdownErrCh:
				return shutdownErr
			default:
				return nil
			}
		}
		return fmt.Errorf("gateway http serve: %w", err)
	}
	return nil
}

// buildHandler 按 tracing、recovery、业务中间件、CORS 和 gateway 的顺序组装处理链
func (server *HttpServer) buildHandler(next http.Handler) http.Handler {
	handler := server.corsMiddleware(next)
	for i := len(server.middlewares) - 1; i >= 0; i-- {
		handler = server.middlewares[i](handler)
	}
	handler = server.recovery(handler)
	return propagateTracing(handler)
}

// corsMiddleware 根据显式配置或旧版兼容模式构造 CORS 中间件
func (server *HttpServer) corsMiddleware(next http.Handler) http.Handler {
	if server.corsPolicy == nil {
		return allowCORSWithHeaders(next, server.corsAllowedHeaders)
	}
	if !server.corsPolicy.config.Enabled {
		return next
	}
	return server.corsPolicy.middleware(next)
}

// shutdownHTTPServer 优雅停止 HTTP 服务，并返回 shutdown 阶段错误
func (server *HttpServer) shutdownHTTPServer(ctx context.Context, httpServer *http.Server, shutdownErrCh chan<- error) {
	<-ctx.Done()
	server.log.Info("gateway.Shutdown now")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultHTTPShutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		shutdownErrCh <- fmt.Errorf("gateway http shutdown: %w", err)
		return
	}
	shutdownErrCh <- nil
}
