package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
