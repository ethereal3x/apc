package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// setupTestTracer 初始化测试用 tracer provider 并返回 span 导出器
func setupTestTracer() *tracetest.InMemoryExporter {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	return exporter
}

// TestHttpClientRequestAndUnmarshalBody 验证正常 HTTP 请求和响应解析流程
func TestHttpClientRequestAndUnmarshalBody(t *testing.T) {
	exporter := setupTestTracer()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "trace-id-1", request.Header.Get("X-Trace-ID"))
		require.Equal(t, "1001", request.URL.Query().Get("account_id"))

		responseWriter.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(responseWriter).Encode(map[string]interface{}{
			"code":       float64(0),
			"account_id": "1001",
		})
		require.NoError(t, err)
	}))
	defer server.Close()

	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl(server.URL).
		SetHeaders(map[string]interface{}{"X-Trace-ID": "trace-id-1"}).
		SetQueryParams(map[string]interface{}{"account_id": 1001})
	resp, err := client.Request()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]interface{}
	err = client.UnmarshalBody(resp, &out)
	require.NoError(t, err)
	require.Equal(t, float64(0), out["code"])
	require.Equal(t, "1001", out["account_id"])
	require.Len(t, exporter.GetSpans(), 1)
}

// TestRequestFinishesSpanWhenResponseBodyCloses 验证响应体关闭时结束请求 span
func TestRequestFinishesSpanWhenResponseBodyCloses(t *testing.T) {
	exporter := setupTestTracer()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		_, err := responseWriter.Write([]byte(`{"ok":true}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl(server.URL)
	resp, err := client.Request()
	require.NoError(t, err)
	require.Len(t, exporter.GetSpans(), 0, "spans are only exported after End() is called")

	err = resp.Body.Close()
	require.NoError(t, err)
	require.Len(t, exporter.GetSpans(), 1, "span should be exported after response body close")
}

// TestRequestFinishesSpanWhenCreateRequestFails 验证请求构造失败时结束请求 span
func TestRequestFinishesSpanWhenCreateRequestFails(t *testing.T) {
	exporter := setupTestTracer()

	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl("://bad-url")
	resp, err := client.Request()
	require.Nil(t, resp)
	require.ErrorContains(t, err, "create http request")
	require.Len(t, exporter.GetSpans(), 1)
	require.False(t, exporter.GetSpans()[0].EndTime.IsZero(), "span should be finished on error")
}

// TestUnmarshalBodyReturnsErrorForNilResponse 验证空响应不会触发 panic
func TestUnmarshalBodyReturnsErrorForNilResponse(t *testing.T) {
	client := NewHttpClient(context.Background()).CloseTracer()
	var out map[string]interface{}

	err := client.UnmarshalBody(nil, &out)
	require.ErrorContains(t, err, "response is nil")
}

// TestDoValidatesMethodAndURL 验证 Do 在 method 或 URL 为空时返回校验错误
func TestDoValidatesMethodAndURL(t *testing.T) {
	t.Run("empty method", func(t *testing.T) {
		client := NewHttpClient(context.Background()).
			SetUrl("http://localhost")
		_, err := client.Do()
		require.Error(t, err)
	})

	t.Run("empty url", func(t *testing.T) {
		client := NewHttpClient(context.Background()).
			SetMethod(http.MethodGet)
		_, err := client.Do()
		require.Error(t, err)
	})
}

// TestDoEquivalentToRequest 验证 Do 与 Request 行为一致
func TestDoEquivalentToRequest(t *testing.T) {
	setupTestTracer()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(responseWriter).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	resp, err := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl(server.URL).
		Do()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

// TestWithHTTPClientInjectsCustomTransport 验证注入自定义 http.Client 可正常工作
func TestWithHTTPClientInjectsCustomTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(responseWriter).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	injected := &http.Client{}
	resp, err := NewHttpClient(context.Background(), WithHTTPClient(injected)).
		SetMethod(http.MethodGet).
		SetUrl(server.URL).
		Do()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

// TestWithTimeoutOption 验证 WithTimeout 设置请求超时
func TestWithTimeoutOption(t *testing.T) {
	client := NewHttpClient(context.Background(), WithTimeout(0))
	require.Equal(t, 0, int(client.Timeout))
}

// TestTimeoutDoesNotMutateInjectedClient 验证请求超时设置不会修改注入的共享 client
func TestTimeoutDoesNotMutateInjectedClient(t *testing.T) {
	injected := &http.Client{Timeout: time.Second}
	client := NewHttpClient(context.Background(), WithHTTPClient(injected)).
		SetTimeout(2 * time.Second)

	require.Equal(t, time.Second, injected.Timeout)
	require.Equal(t, 2*time.Second, client.Timeout)
}

// TestWithEnableTraceFalse 验证 WithEnableTrace(false) 禁用 tracing
func TestWithEnableTraceFalse(t *testing.T) {
	exporter := setupTestTracer()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = responseWriter.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewHttpClient(context.Background(), WithEnableTrace(false)).
		SetMethod(http.MethodGet).
		SetUrl(server.URL)
	resp, err := client.Do()
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, exporter.GetSpans(), 0, "no spans when tracing disabled")
}

// TestSetBodyEmptyReturnsError 验证空 body 设置记录错误
func TestSetBodyEmptyReturnsError(t *testing.T) {
	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodPost).
		SetUrl("http://localhost").
		SetBody(map[string]interface{}{})
	_, err := client.Do()
	require.ErrorIs(t, err, ErrParamsEmpty)
}

// TestSetQueryParamsEmptyReturnsError 验证空查询参数记录错误
func TestSetQueryParamsEmptyReturnsError(t *testing.T) {
	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl("http://localhost").
		SetQueryParams(map[string]interface{}{})
	_, err := client.Do()
	require.ErrorIs(t, err, ErrParamsEmpty)
}

// TestRequestErrorReturnsWrappedError 验证网络错误返回包装后的 error
func TestRequestErrorReturnsWrappedError(t *testing.T) {
	setupTestTracer()
	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl("http://127.0.0.1:0/nonexistent")
	_, err := client.Do()
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrParamsEmpty))
}

// TestHttpClientTracingDoesNotRecordSensitiveData 验证 tracing 不记录凭据、token 和请求响应正文
func TestHttpClientTracingDoesNotRecordSensitiveData(t *testing.T) {
	exporter := setupTestTracer()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer secret-authorization", request.Header.Get("Authorization"))
		responseWriter.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(responseWriter).Encode(map[string]string{
			"access_token": "secret-response-token",
		}))
	}))
	defer server.Close()

	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodPost).
		SetUrl(server.URL + "/token").
		SetHeaders(map[string]interface{}{
			"Authorization": "Bearer secret-authorization",
			"Cookie":        "session=secret-cookie",
		}).
		SetQueryParams(map[string]interface{}{"code": "secret-query-code"}).
		SetBody(map[string]interface{}{"client_secret": "secret-request-body"})
	response, err := client.Do()
	require.NoError(t, err)
	var payload map[string]string
	require.NoError(t, client.UnmarshalBody(response, &payload))
	require.Equal(t, "secret-response-token", payload["access_token"])

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	spanData := fmt.Sprintf("%+v", spans[0])
	for _, secret := range []string{
		"secret-authorization",
		"secret-cookie",
		"secret-query-code",
		"secret-request-body",
		"secret-response-token",
	} {
		require.NotContains(t, spanData, secret)
	}
	require.NotContains(t, spans[0].Name, "secret-query-code")
}

// TestHttpClientErrorTracingDoesNotRecordSensitiveURL 验证网络错误 tracing 不记录查询参数中的 token
func TestHttpClientErrorTracingDoesNotRecordSensitiveURL(t *testing.T) {
	exporter := setupTestTracer()
	_, err := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl("http://127.0.0.1:0/callback?token=secret-error-token").
		Do()
	require.Error(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.NotContains(t, fmt.Sprintf("%+v", spans[0]), "secret-error-token")
}
