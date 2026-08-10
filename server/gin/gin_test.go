package gin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/ethereal3x/apc/errs"
	apcserver "github.com/ethereal3x/apc/server"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testConfigYAML = `
server:
  gin_addr: "127.0.0.1:0"
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

// newTestServer 创建注册了测试路由的 gin 服务
func newTestServer(t *testing.T) *GinServer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	server := NewGinServer()
	server.SetRegisterFunc(func(_ context.Context, engine *gin.Engine) {
		engine.GET("/ping", func(c *gin.Context) {
			c.String(http.StatusOK, "pong")
		})
		engine.GET("/panic", func(*gin.Context) {
			panic("secret-panic-value")
		})
	})
	return server
}

// TestGinServerServesRoute 验证 gin 服务随 ctx 启动并响应请求，ctx 取消后优雅退出
func TestGinServerServesRoute(t *testing.T) {
	setupTestConfig(t)
	server := newTestServer(t)
	server.SetAddress("127.0.0.1:18081")

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx) }()

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://127.0.0.1:18081/ping")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-errCh)
}

// TestGinRecoveryDoesNotLeakPanic 验证 panic 被 apc recovery 捕获并返回固定 500
func TestGinRecoveryDoesNotLeakPanic(t *testing.T) {
	setupTestConfig(t)
	server := newTestServer(t)
	server.registerFunc(context.Background(), server.Engine())
	handler := server.buildHandler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.JSONEq(t, `{"error":"internal server error"}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "secret-panic-value")
	require.Len(t, recorder.Header().Get("Tracing-Id"), 32)
}

// TestGinCORSAllowedOrigin 验证 CORS 策略在 gin 处理链中生效
func TestGinCORSAllowedOrigin(t *testing.T) {
	setupTestConfig(t)
	server := newTestServer(t)
	server.registerFunc(context.Background(), server.Engine())
	require.NoError(t, server.SetCORSConfig(config2CORS()))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	request.Header.Set("Origin", "https://app.example.com")
	server.buildHandler().ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "https://app.example.com", recorder.Header().Get("Access-Control-Allow-Origin"))
}

func config2CORS() apcserver.CORSConfig {
	return apcserver.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
	}
}

// TestWriteErrorMapsBizError 验证 BizError 被映射为业务码 JSON 响应
func TestWriteErrorMapsBizError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/biz", func(c *gin.Context) {
		WriteError(c, errs.New(1001, "biz failed"))
	})
	engine.GET("/unknown", func(c *gin.Context) {
		WriteError(c, errors.New("internal detail"))
	})

	bizRecorder := httptest.NewRecorder()
	engine.ServeHTTP(bizRecorder, httptest.NewRequest(http.MethodGet, "/biz", nil))
	require.Equal(t, http.StatusOK, bizRecorder.Code)
	require.JSONEq(t, `{"code":1001,"message":"biz failed"}`, bizRecorder.Body.String())

	unknownRecorder := httptest.NewRecorder()
	engine.ServeHTTP(unknownRecorder, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	require.Equal(t, http.StatusInternalServerError, unknownRecorder.Code)
	require.JSONEq(t, `{"code":-1,"message":"internal server error"}`, unknownRecorder.Body.String())
	require.NotContains(t, unknownRecorder.Body.String(), "internal detail")
}

// TestGinServerRejectsEmptyRoutes 验证未注册路由时启动报错
func TestGinServerRejectsEmptyRoutes(t *testing.T) {
	setupTestConfig(t)
	gin.SetMode(gin.TestMode)
	server := NewGinServer()
	err := server.Run(context.Background())
	require.Error(t, err)
}
