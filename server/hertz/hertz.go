package hertz

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hertzserver "github.com/cloudwego/hertz/pkg/app/server"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/ethereal3x/apc/config"
	apclogger "github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/server"
	apctracing "github.com/ethereal3x/apc/tracing"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
)

// HertzRecoveryHandler 自定义 hertz panic 响应处理策略
type HertzRecoveryHandler func(ctx context.Context, c *app.RequestContext, recoveryContext server.RecoveryContext)

// HertzServer 基于 hertz 的 HTTP 服务，复用 apc 的 tracing、CORS 策略和 RecoveryContext，
// 与 GrpcServer/HttpServer/GinServer 一样实现 server.Runner 可被统一编排
type HertzServer struct {
	server.Server
	engine          *hertzserver.Hertz
	registerFunc    func(context.Context, *hertzserver.Hertz)
	middlewares     []app.HandlerFunc
	corsPolicy      *server.CORSPolicy
	recoveryHandler HertzRecoveryHandler
}

// SetRegisterFunc 设置 hertz 路由注册回调函数
func (s *HertzServer) SetRegisterFunc(registerFunc func(context.Context, *hertzserver.Hertz)) {
	s.registerFunc = registerFunc
}

// Engine 返回底层 hertz 引擎，供需要直接操作引擎的场景使用
func (s *HertzServer) Engine() *hertzserver.Hertz {
	return s.engine
}

// SetMiddleware 追加 hertz 原生中间件，注册在 apc 内置中间件之后
func (s *HertzServer) SetMiddleware(middlewares ...app.HandlerFunc) {
	s.middlewares = append(s.middlewares, middlewares...)
}

// SetCORSConfig 校验并设置完整 CORS 策略，Enabled=false 时不处理任何跨域请求
func (s *HertzServer) SetCORSConfig(config server.CORSConfig) error {
	policy, err := server.NewCORSPolicy(config)
	if err != nil {
		return err
	}
	s.corsPolicy = policy
	return nil
}

// SetRecoveryHandler 设置自定义 hertz panic 响应处理策略
func (s *HertzServer) SetRecoveryHandler(handler HertzRecoveryHandler) {
	s.recoveryHandler = handler
}

// NewHertzServer 创建 hertz 服务实例，默认地址取自配置 server.hertz_addr，
// options 会叠加在默认 WithHostPorts 之后，可覆盖监听地址等引擎参数
func NewHertzServer(options ...hertzconfig.Option) *HertzServer {
	conf := config.GetConf()
	engine := hertzserver.Default(append([]hertzconfig.Option{hertzserver.WithHostPorts(conf.Server.HertzAddr)}, options...)...)
	return &HertzServer{
		Server: server.NewServer(conf.Server.HertzAddr),
		engine: engine,
	}
}

// Run 装配中间件、注册路由并启动 hertz 服务，监听 ctx 取消信号优雅关闭，
// hertz 要求 Use 先于路由注册，返回服务运行和关闭阶段的错误
func (s *HertzServer) Run(ctx context.Context) error {
	s.engine.Use(s.buildMiddlewares()...)
	if s.registerFunc != nil {
		s.registerFunc(ctx, s.engine)
	}

	errCh := make(chan error, 1)
	go func() {
		s.engine.Spin()
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.Logger().Info(fmt.Sprintf("hertz service %s shutdown now", s.Address()))
		// Shutdown 按 ExitWaitTimeout 优雅排空连接，Close 是零超时硬关闭不适合优雅退出
		if err := s.engine.Shutdown(context.WithoutCancel(ctx)); err != nil {
			return fmt.Errorf("hertz shutdown %s: %w", s.Address(), err)
		}
		return nil
	}
}

// buildMiddlewares 按 tracing、recovery、业务中间件、CORS 的顺序组装 hertz 中间件链，
// 语义与 grpc-gateway/gin 的处理链保持一致
func (s *HertzServer) buildMiddlewares() []app.HandlerFunc {
	middlewares := []app.HandlerFunc{
		tracingMiddleware(),
		s.recoveryMiddleware(),
	}
	middlewares = append(middlewares, s.middlewares...)
	if s.corsPolicy != nil && s.corsPolicy.Enabled() {
		middlewares = append(middlewares, corsMiddleware(s.corsPolicy))
	}
	return middlewares
}

// tracingMiddleware 从 hertz 请求头提取上游 tracing 上下文并创建入口 span，
// 同时向响应头回写 Tracing-Id
func tracingMiddleware() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		ctx = otel.GetTextMapPropagator().Extract(ctx, &requestHeaderCarrier{header: &c.Request.Header})
		ctx, span := apctracing.Start(ctx, fmt.Sprintf("HTTP %s %s", c.Method(), c.Path()))
		defer span.End()
		c.Response.Header.Set("Tracing-Id", apctracing.TraceID(ctx))
		c.Next(ctx)
	}
}

// recoveryMiddleware 捕获 hertz handler panic，记录结构化日志后执行安全恢复策略，
// 默认返回不包含内部细节的固定 500 响应
func (s *HertzServer) recoveryMiddleware() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		defer func() {
			panicValue := recover()
			if panicValue == nil {
				return
			}
			recoveryContext := server.NewRecoveryContext(ctx, panicValue)
			logRecovery(s.Logger(), recoveryContext)
			if s.recoveryHandler != nil {
				s.recoveryHandler(ctx, c, recoveryContext)
				return
			}
			c.AbortWithMsg(defaultRecoveryBody, http.StatusInternalServerError)
		}()
		c.Next(ctx)
	}
}

// corsMiddleware 将 apc CORSPolicy 适配到 hertz 请求上下文
func corsMiddleware(policy *server.CORSPolicy) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		origin := strings.TrimSpace(string(c.Request.Header.Peek("Origin")))
		if origin == "" || !policy.IsOriginAllowed(origin) {
			c.Next(ctx)
			return
		}
		isPreflight := string(c.Method()) == http.MethodOptions && len(c.Request.Header.Peek("Access-Control-Request-Method")) > 0
		if isPreflight && !policy.IsPreflightAllowed(
			string(c.Request.Header.Peek("Access-Control-Request-Method")),
			string(c.Request.Header.Peek("Access-Control-Request-Headers")),
		) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		writeCORSResponseHeaders(&c.Response.Header, policy, origin)
		if !isPreflight {
			c.Next(ctx)
			return
		}
		c.Response.Header.Set("Access-Control-Allow-Methods", strings.Join(policy.AllowedMethods(), ","))
		c.Response.Header.Set("Access-Control-Allow-Headers", strings.Join(policy.AllowedHeaders(), ","))
		if maxAgeSeconds := int64(policy.MaxAge() / time.Second); maxAgeSeconds > 0 {
			c.Response.Header.Set("Access-Control-Max-Age", strconv.FormatInt(maxAgeSeconds, 10))
		}
		c.AbortWithStatus(http.StatusNoContent)
	}
}

// writeCORSResponseHeaders 复用 CORSPolicy 的响应头写入逻辑
func writeCORSResponseHeaders(header *protocol.ResponseHeader, policy *server.CORSPolicy, origin string) {
	headers := make(http.Header)
	policy.WriteResponseHeaders(headers, origin)
	for key, values := range headers {
		for _, value := range values {
			header.Add(key, value)
		}
	}
}

// logRecovery 将 panic 值和完整堆栈写入服务端结构化日志
func logRecovery(log apclogger.Logger, recoveryContext server.RecoveryContext) {
	if log == nil {
		return
	}
	log.ContextError(
		recoveryContext.Context,
		"server recovered panic",
		zap.Any("panic", recoveryContext.PanicValue),
		zap.ByteString("stack", recoveryContext.Stack),
		zap.String("trace_id", recoveryContext.TraceID),
	)
}

const defaultRecoveryBody = `{"error":"internal server error"}`

// 静态校验 HertzServer 实现 server.Runner
var _ server.Runner = (*HertzServer)(nil)
