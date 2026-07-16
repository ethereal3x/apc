package server

import (
	"context"
	"net/http"
	"runtime/debug"

	apctracing "github.com/ethereal3x/apc/tracing"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultRecoveryMessage = "internal server error"
const defaultHTTPRecoveryBody = `{"error":"internal server error"}`

// RecoveryContext 描述仅供服务端恢复处理器使用的 panic 上下文
type RecoveryContext struct {
	Context    context.Context
	PanicValue any
	Stack      []byte
	TraceID    string
}

// HTTPRecoveryHandler 自定义 HTTP panic 响应处理策略
type HTTPRecoveryHandler func(http.ResponseWriter, *http.Request, RecoveryContext)

// GRPCRecoveryHandler 自定义 gRPC panic 错误映射策略
type GRPCRecoveryHandler func(RecoveryContext) error

// newRecoveryContext 捕获 panic 值、当前 goroutine 完整堆栈和 trace ID
func newRecoveryContext(ctx context.Context, panicValue any) RecoveryContext {
	return RecoveryContext{
		Context:    ctx,
		PanicValue: panicValue,
		Stack:      debug.Stack(),
		TraceID:    apctracing.TraceID(ctx),
	}
}

// logRecovery 将 panic 值和完整堆栈写入服务端结构化日志
func (server *Server) logRecovery(recoveryContext RecoveryContext) {
	if server.log == nil {
		return
	}
	server.log.ContextError(
		recoveryContext.Context,
		"server recovered panic",
		zap.Any("panic", recoveryContext.PanicValue),
		zap.ByteString("stack", recoveryContext.Stack),
		zap.String("trace_id", recoveryContext.TraceID),
	)
}

// recovery 捕获 HTTP handler panic 并执行安全恢复策略
func (server *HttpServer) recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		defer func() {
			panicValue := recover()
			if panicValue == nil {
				return
			}
			recoveryContext := newRecoveryContext(request.Context(), panicValue)
			server.logRecovery(recoveryContext)
			if server.recoveryHandler != nil {
				server.recoveryHandler(responseWriter, request, recoveryContext)
				return
			}
			server.writeDefaultRecoveryResponse(responseWriter, request.Context())
		}()
		next.ServeHTTP(responseWriter, request)
	})
}

// writeDefaultRecoveryResponse 返回不包含 panic 和内部实现细节的固定 HTTP 500 响应
func (server *HttpServer) writeDefaultRecoveryResponse(responseWriter http.ResponseWriter, ctx context.Context) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(http.StatusInternalServerError)
	if _, err := responseWriter.Write([]byte(defaultHTTPRecoveryBody)); err != nil && server.log != nil {
		server.log.ContextError(ctx, "write HTTP recovery response failed", zap.Error(err))
	}
}

// grpcRecoveryHandler 捕获 gRPC panic 并返回不包含内部细节的错误
func (server *GrpcServer) grpcRecoveryHandler(ctx context.Context, panicValue any) error {
	recoveryContext := newRecoveryContext(ctx, panicValue)
	server.logRecovery(recoveryContext)
	if server.recoveryHandler != nil {
		return server.recoveryHandler(recoveryContext)
	}
	return status.Error(codes.Internal, defaultRecoveryMessage)
}
