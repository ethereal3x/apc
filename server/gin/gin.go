package gin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/ethereal3x/apc/server"
	"github.com/gin-gonic/gin"
)

const defaultWriteTimeout = 15 * time.Second

// GinServer 基于 gin 的 HTTP 服务，复用 apc 的 tracing、recovery、CORS 处理链和优雅关闭，
// 与 GrpcServer/HttpServer 一样实现 server.Runner 可被统一编排
type GinServer struct {
	server.Server
	engine          *gin.Engine
	registerFunc    func(context.Context, *gin.Engine)
	writeTimeout    time.Duration
	middlewares     []func(http.Handler) http.Handler
	corsPolicy      *server.CORSPolicy
	recoveryHandler server.HTTPRecoveryHandler
}

// SetRegisterFunc 设置 gin 路由注册回调函数
func (s *GinServer) SetRegisterFunc(registerFunc func(context.Context, *gin.Engine)) {
	s.registerFunc = registerFunc
}

// Engine 返回底层 gin.Engine，供需要直接操作引擎的场景使用
func (s *GinServer) Engine() *gin.Engine {
	return s.engine
}

// SetWriteTimeout 设置 HTTP 写超时，0 表示不限制（用于流式响应）
func (s *GinServer) SetWriteTimeout(timeout time.Duration) {
	s.writeTimeout = timeout
}

// SetMiddleware 设置 net/http 中间件链，按顺序包裹在 CORS 和 gin engine 外层
func (s *GinServer) SetMiddleware(middlewares ...func(http.Handler) http.Handler) {
	s.middlewares = append(s.middlewares, middlewares...)
}

// SetGinMiddleware 追加 gin 原生中间件，注册在 gin 引擎内部
func (s *GinServer) SetGinMiddleware(middlewares ...gin.HandlerFunc) {
	s.engine.Use(middlewares...)
}

// SetCORSConfig 校验并设置完整 CORS 策略，Enabled=false 时不处理任何跨域请求
func (s *GinServer) SetCORSConfig(config server.CORSConfig) error {
	policy, err := server.NewCORSPolicy(config)
	if err != nil {
		return err
	}
	s.corsPolicy = policy
	return nil
}

// SetRecoveryHandler 设置自定义 HTTP panic 响应处理策略
func (s *GinServer) SetRecoveryHandler(handler server.HTTPRecoveryHandler) {
	s.recoveryHandler = handler
}

// NewGinServer 创建 gin 服务实例，默认地址取自配置 server.gin_addr；
// 使用 gin.New() 避免内置 Recovery/Logger 与 apc 处理链重复
func NewGinServer(options ...gin.OptionFunc) *GinServer {
	return &GinServer{
		Server:       server.NewServer(config.GetConf().Server.GinAddr),
		engine:       gin.New(options...),
		writeTimeout: defaultWriteTimeout,
	}
}

// buildHandler 组装与 grpc-gateway 网关一致的处理链：tracing → recovery → 业务中间件 → CORS → gin engine
func (s *GinServer) buildHandler() http.Handler {
	var cors http.Handler = s.engine
	if s.corsPolicy != nil && s.corsPolicy.Enabled() {
		cors = s.corsPolicy.Middleware(s.engine)
	}
	return server.BuildHTTPHandlerChain(s.engine, s.middlewares, cors, s.Logger(), s.recoveryHandler)
}

// Run 注册路由并启动 HTTP 服务，监听 ctx 取消信号优雅关闭，返回服务运行期间的错误
func (s *GinServer) Run(ctx context.Context) error {
	if s.registerFunc != nil {
		s.registerFunc(ctx, s.engine)
	}
	if len(s.engine.Routes()) == 0 {
		return fmt.Errorf("gin server %s: no route registered", s.Address())
	}
	return server.ListenAndServeHTTP(ctx, s.Address(), s.buildHandler(), 0, s.writeTimeout, 0, s.Logger())
}
