package hertz

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hertzserver "github.com/cloudwego/hertz/pkg/app/server"
	hertzut "github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/ethereal3x/apc/config"
	"github.com/ethereal3x/apc/errs"
	"github.com/ethereal3x/apc/server"
	"github.com/stretchr/testify/require"
)

const testConfigYAML = `
server:
  hertz_addr: "127.0.0.1:0"
plugin:
  log:
    level: "error"
`

// setupTestConfig 写入最小测试配置，供 config.GetConf 懒加载
func setupTestConfig(t *testing.T) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(testConfigYAML), 0o644))
	t.Setenv(config.ConfigPathEnvKey, configPath)
}

// newTestServer 创建并装配 apc 中间件的 hertz 服务，hertz 要求 Use 先于路由注册，
// extraMiddlewares 会叠加在 apc 内置中间件之后
func newTestServer(t *testing.T, address string, extraMiddlewares ...app.HandlerFunc) *HertzServer {
	t.Helper()
	engine := hertzserver.New(hertzserver.WithHostPorts(address))
	hertzServer := &HertzServer{
		Server: server.NewServer(address),
		engine: engine,
	}
	engine.Use(append(hertzServer.buildMiddlewares(), extraMiddlewares...)...)
	return hertzServer
}

// registerTestRoutes 在已装配中间件的引擎上注册测试路由
func registerTestRoutes(engine *hertzserver.Hertz) {
	engine.GET("/ping", func(_ context.Context, c *app.RequestContext) {
		c.String(http.StatusOK, "pong")
	})
	engine.GET("/panic", func(_ context.Context, c *app.RequestContext) {
		panic("secret-panic-value")
	})
}

// TestHertzRecoveryDoesNotLeakPanic 验证 panic 被 apc recovery 捕获并返回固定 500
func TestHertzRecoveryDoesNotLeakPanic(t *testing.T) {
	setupTestConfig(t)
	hertzServer := newTestServer(t, "127.0.0.1:0")
	registerTestRoutes(hertzServer.engine)

	recorder := hertzut.PerformRequest(hertzServer.engine.Engine, http.MethodGet, "http://localhost/panic", nil)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.JSONEq(t, defaultRecoveryBody, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "secret-panic-value")
}

// TestHertzCORSAllowedOrigin 验证 CORS 策略在 hertz 中间件链中生效
func TestHertzCORSAllowedOrigin(t *testing.T) {
	setupTestConfig(t)
	hertzServer := newTestServer(t, "127.0.0.1:0")
	require.NoError(t, hertzServer.SetCORSConfig(server.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
	}))
	hertzServer.engine.Use(corsMiddleware(hertzServer.corsPolicy))
	registerTestRoutes(hertzServer.engine)

	recorder := hertzut.PerformRequest(hertzServer.engine.Engine, http.MethodGet, "http://localhost/ping", nil,
		hertzut.Header{Key: "Origin", Value: "https://app.example.com"})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "https://app.example.com", string(recorder.Header().Peek("Access-Control-Allow-Origin")))
}

// TestHertzCORSPreflightRejectsDisallowedHeader 验证预检请求携带非白名单 Header 时拒绝授权
func TestHertzCORSPreflightRejectsDisallowedHeader(t *testing.T) {
	setupTestConfig(t)
	hertzServer := newTestServer(t, "127.0.0.1:0")
	require.NoError(t, hertzServer.SetCORSConfig(server.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{http.MethodPost},
		AllowedHeaders: []string{"Content-Type"},
	}))
	hertzServer.engine.Use(corsMiddleware(hertzServer.corsPolicy))
	registerTestRoutes(hertzServer.engine)

	recorder := hertzut.PerformRequest(hertzServer.engine.Engine, http.MethodOptions, "http://localhost/ping", nil,
		hertzut.Header{Key: "Origin", Value: "https://app.example.com"},
		hertzut.Header{Key: "Access-Control-Request-Method", Value: http.MethodPost},
		hertzut.Header{Key: "Access-Control-Request-Headers", Value: "X-Internal-Token"})

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Empty(t, recorder.Header().Peek("Access-Control-Allow-Origin"))
}

// TestWriteErrorMapsBizError 验证 BizError 被映射为业务码 JSON 响应
func TestWriteErrorMapsBizError(t *testing.T) {
	setupTestConfig(t)
	engine := hertzserver.New(hertzserver.WithHostPorts("127.0.0.1:0"))
	engine.GET("/biz", func(ctx context.Context, c *app.RequestContext) {
		WriteError(ctx, c, errs.New(1001, "biz failed"))
	})
	engine.GET("/unknown", func(ctx context.Context, c *app.RequestContext) {
		WriteError(ctx, c, errors.New("internal detail"))
	})

	bizRecorder := hertzut.PerformRequest(engine.Engine, http.MethodGet, "http://localhost/biz", nil)
	require.Equal(t, http.StatusOK, bizRecorder.Code)
	require.JSONEq(t, `{"code":1001,"message":"biz failed"}`, bizRecorder.Body.String())

	unknownRecorder := hertzut.PerformRequest(engine.Engine, http.MethodGet, "http://localhost/unknown", nil)
	require.Equal(t, http.StatusInternalServerError, unknownRecorder.Code)
	require.JSONEq(t, `{"code":-1,"message":"internal server error"}`, unknownRecorder.Body.String())
	require.NotContains(t, unknownRecorder.Body.String(), "internal detail")
}

// TestHertzServerGracefulShutdown 验证 hertz 服务随 ctx 启动并响应请求，ctx 取消后优雅退出
func TestHertzServerGracefulShutdown(t *testing.T) {
	setupTestConfig(t)
	hertzServer := NewHertzServer(hertzserver.WithHostPorts("127.0.0.1:18092"))
	hertzServer.SetAddress("127.0.0.1:18092")
	hertzServer.SetRegisterFunc(func(_ context.Context, engine *hertzserver.Hertz) {
		engine.GET("/ping", func(_ context.Context, c *app.RequestContext) {
			c.String(http.StatusOK, "pong")
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- hertzServer.Run(ctx) }()

	client := &http.Client{}
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://127.0.0.1:18092/ping")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 3*time.Second, 20*time.Millisecond)
	// 关闭 keep-alive 空闲连接，避免优雅关闭等待连接排空超时
	client.CloseIdleConnections()

	cancel()
	require.NoError(t, <-errCh)
}
