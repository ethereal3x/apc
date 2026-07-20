package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testPanicSecret = "database-password=never-return-this"

// TestHTTPRecoveryDoesNotLeakPanic 验证默认 HTTP recovery 返回固定 500 且不泄漏 panic
func TestHTTPRecoveryDoesNotLeakPanic(t *testing.T) {
	server := &HttpServer{}
	handler := server.buildHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(testPanicSecret)
	}))
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":"internal server error"}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), testPanicSecret)
	require.Len(t, recorder.Header().Get("Tracing-Id"), 32)
}

// TestHTTPRecoverySupportsCustomHandler 验证 HTTP recovery 可注入自定义安全响应策略
func TestHTTPRecoverySupportsCustomHandler(t *testing.T) {
	server := &HttpServer{}
	var captured RecoveryContext
	server.SetRecoveryHandler(func(responseWriter http.ResponseWriter, _ *http.Request, recoveryContext RecoveryContext) {
		captured = recoveryContext
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
	})
	handler := server.recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(testPanicSecret)
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, testPanicSecret, captured.PanicValue)
	require.Contains(t, string(captured.Stack), "TestHTTPRecoverySupportsCustomHandler")
}

// TestGRPCRecoveryDoesNotLeakPanic 验证默认 gRPC recovery 返回 codes.Internal 且不泄漏 panic
func TestGRPCRecoveryDoesNotLeakPanic(t *testing.T) {
	server := &GrpcServer{}
	_, unaryInterceptors := server.genServerOptions()
	recoveryInterceptor := unaryInterceptors[1]
	response, err := recoveryInterceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Panic"},
		func(context.Context, any) (any, error) {
			panic(testPanicSecret)
		},
	)

	require.Nil(t, response)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, defaultRecoveryMessage, status.Convert(err).Message())
	require.NotContains(t, err.Error(), testPanicSecret)
}

// TestGRPCRecoverySupportsCustomHandler 验证 gRPC recovery 可注入自定义错误映射策略
func TestGRPCRecoverySupportsCustomHandler(t *testing.T) {
	server := &GrpcServer{}
	var captured RecoveryContext
	server.SetRecoveryHandler(func(recoveryContext RecoveryContext) error {
		captured = recoveryContext
		return status.Error(codes.Unavailable, "safe custom recovery")
	})
	_, unaryInterceptors := server.genServerOptions()
	_, err := unaryInterceptors[1](
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Panic"},
		func(context.Context, any) (any, error) {
			panic(testPanicSecret)
		},
	)

	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, testPanicSecret, captured.PanicValue)
	require.NotEmpty(t, captured.Stack)
}

// TestSetCORSConfigRejectsWildcardCredentials 验证携带凭据时拒绝通配 Origin
func TestSetCORSConfigRejectsWildcardCredentials(t *testing.T) {
	server := &HttpServer{}
	err := server.SetCORSConfig(CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})

	require.ErrorIs(t, err, ErrCORSWildcardCredentials)
}

// TestCORSAllowedOrigin 验证白名单 Origin 精确回写凭据和暴露响应头
func TestCORSAllowedOrigin(t *testing.T) {
	server := &HttpServer{}
	err := server.SetCORSConfig(CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"https://app.example.com"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
	})
	require.NoError(t, err)
	handler := server.corsMiddleware(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.Header.Set("Origin", "https://app.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "https://app.example.com", recorder.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
	require.Equal(t, "X-Request-ID", recorder.Header().Get("Access-Control-Expose-Headers"))
	require.Contains(t, recorder.Header().Values("Vary"), "Origin")
}

// TestCORSRejectedOrigin 验证非白名单 Origin 不返回任何授权 CORS Header
func TestCORSRejectedOrigin(t *testing.T) {
	server := &HttpServer{}
	require.NoError(t, server.SetCORSConfig(CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
	}))
	nextCalled := false
	handler := server.corsMiddleware(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		responseWriter.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.Header.Set("Origin", "https://attacker.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.True(t, nextCalled)
	require.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
	require.Empty(t, recorder.Header().Get("Access-Control-Allow-Credentials"))
}

// TestCORSPreflightUsesConfiguredPolicy 验证预检请求校验并返回同一套 CORS 策略
func TestCORSPreflightUsesConfiguredPolicy(t *testing.T) {
	server := &HttpServer{}
	require.NoError(t, server.SetCORSConfig(CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{http.MethodPost},
		AllowedHeaders:   []string{"Content-Type", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           10 * time.Minute,
	}))
	nextCalled := false
	handler := server.corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type, x-csrf-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.False(t, nextCalled)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "https://app.example.com", recorder.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "POST", recorder.Header().Get("Access-Control-Allow-Methods"))
	require.Equal(t, "Content-Type,X-CSRF-Token", recorder.Header().Get("Access-Control-Allow-Headers"))
	require.Equal(t, "600", recorder.Header().Get("Access-Control-Max-Age"))
}

// TestCORSDisabledDoesNotHandlePreflight 验证关闭 CORS 后不写 Header 且不拦截 OPTIONS
func TestCORSDisabledDoesNotHandlePreflight(t *testing.T) {
	server := &HttpServer{}
	require.NoError(t, server.SetCORSConfig(CORSConfig{Enabled: false}))
	nextCalled := false
	handler := server.corsMiddleware(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		responseWriter.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.True(t, nextCalled)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	for header := range recorder.Header() {
		require.False(t, strings.HasPrefix(header, "Access-Control-"))
	}
}

// TestLegacyCORSAllowedHeadersRemainsCompatible 验证旧版 CORS 预检回显 Origin 并支持 credentials
func TestLegacyCORSAllowedHeadersRemainsCompatible(t *testing.T) {
	server := &HttpServer{}
	server.SetCORSAllowedHeaders([]string{"Content-Type", "X-Legacy-Token"})
	handler := server.corsMiddleware(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://legacy.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "https://legacy.example.com", recorder.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
	require.Equal(t, "Content-Type,X-Legacy-Token", recorder.Header().Get("Access-Control-Allow-Headers"))
}

// TestCORSPreflightRejectsDisallowedHeader 验证预检请求携带非白名单 Header 时拒绝授权
func TestCORSPreflightRejectsDisallowedHeader(t *testing.T) {
	server := &HttpServer{}
	require.NoError(t, server.SetCORSConfig(CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{http.MethodPost},
		AllowedHeaders: []string{"Content-Type"},
	}))
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "X-Internal-Token")
	recorder := httptest.NewRecorder()

	server.corsMiddleware(http.NotFoundHandler()).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
}

// TestBusinessMiddlewareRunsBeforeAPCPreflight 验证 APC 不会在业务中间件之前终止预检请求
func TestBusinessMiddlewareRunsBeforeAPCPreflight(t *testing.T) {
	server := &HttpServer{}
	require.NoError(t, server.SetCORSConfig(CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{http.MethodPost},
	}))
	businessMiddlewareCalled := false
	server.SetMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			businessMiddlewareCalled = true
			next.ServeHTTP(responseWriter, request)
		})
	})
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	server.buildHandler(http.NotFoundHandler()).ServeHTTP(recorder, request)

	require.True(t, businessMiddlewareCalled)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}
