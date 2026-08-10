package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereal3x/apc/logger"
	apctracing "github.com/ethereal3x/apc/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	defaultReadHeaderTimeout = 15 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
)

// TracingMiddleware 从请求头提取上游 tracing 上下文并创建入口 span，
// 同时向响应头回写 Tracing-Id，供 gin/grpc-gateway 等任意 http.Handler 复用
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		ctx, span := apctracing.Start(ctx, fmt.Sprintf("HTTP-gRPC %s %s", request.Method, request.URL.Path))
		defer span.End()
		responseWriter.Header().Set("Tracing-Id", apctracing.TraceID(ctx))
		next.ServeHTTP(responseWriter, request.WithContext(ctx))
	})
}

// RecoveryMiddleware 捕获 handler panic，记录结构化日志后执行安全恢复策略；
// handler 为 nil 时返回不包含内部细节的固定 500 响应
func RecoveryMiddleware(log logger.Logger, handler HTTPRecoveryHandler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			defer func() {
				panicValue := recover()
				if panicValue == nil {
					return
				}
				recoveryContext := NewRecoveryContext(request.Context(), panicValue)
				logRecovery(log, recoveryContext)
				if handler != nil {
					handler(responseWriter, request, recoveryContext)
					return
				}
				writeDefaultRecoveryResponse(log, responseWriter, request.Context())
			}()
			next.ServeHTTP(responseWriter, request)
		})
	}
}

// BuildHTTPHandlerChain 按 CORS、业务中间件、recovery、tracing 的顺序组装处理链，
// 与 grpc-gateway 网关的装配顺序保持一致，供 gin 等 http.Handler 类框架适配复用
func BuildHTTPHandlerChain(next http.Handler, middlewares []func(http.Handler) http.Handler, cors http.Handler, log logger.Logger, recoveryHandler HTTPRecoveryHandler) http.Handler {
	handler := cors
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	handler = RecoveryMiddleware(log, recoveryHandler)(handler)
	return TracingMiddleware(handler)
}

// ListenAndServeHTTP 以标准库 http.Server 承载 handler，监听 ctx 取消信号执行优雅关闭，
// 返回服务运行和 shutdown 阶段的错误
func ListenAndServeHTTP(ctx context.Context, address string, handler http.Handler, readHeaderTimeout, writeTimeout, shutdownTimeout time.Duration, log logger.Logger) error {
	if readHeaderTimeout <= 0 {
		readHeaderTimeout = defaultReadHeaderTimeout
	}
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
	}

	shutdownErrCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		if log != nil {
			log.Info(fmt.Sprintf("http service %s shutdown now", address))
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			shutdownErrCh <- fmt.Errorf("http shutdown %s: %w", address, err)
			return
		}
		shutdownErrCh <- nil
	}()

	if err := httpServer.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return <-shutdownErrCh
		}
		return fmt.Errorf("http serve %s: %w", address, err)
	}
	return nil
}
