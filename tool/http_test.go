package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/require"
)

// TestHttpClientRequestAndUnmarshalBody 验证正常 HTTP 请求和响应解析流程
func TestHttpClientRequestAndUnmarshalBody(t *testing.T) {
	tracer := mocktracer.New()
	opentracing.SetGlobalTracer(tracer)
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
	require.Len(t, tracer.FinishedSpans(), 1)
}

// TestRequestFinishesSpanWhenResponseBodyCloses 验证响应体关闭时结束请求 span
func TestRequestFinishesSpanWhenResponseBodyCloses(t *testing.T) {
	tracer := mocktracer.New()
	opentracing.SetGlobalTracer(tracer)
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
	require.Len(t, tracer.FinishedSpans(), 0)

	err = resp.Body.Close()
	require.NoError(t, err)
	require.Len(t, tracer.FinishedSpans(), 1)
}

// TestRequestFinishesSpanWhenCreateRequestFails 验证请求构造失败时结束请求 span
func TestRequestFinishesSpanWhenCreateRequestFails(t *testing.T) {
	tracer := mocktracer.New()
	opentracing.SetGlobalTracer(tracer)

	client := NewHttpClient(context.Background()).
		SetMethod(http.MethodGet).
		SetUrl("://bad-url")
	resp, err := client.Request()
	require.Nil(t, resp)
	require.ErrorContains(t, err, "create http request")
	require.Len(t, tracer.FinishedSpans(), 1)
}

// TestUnmarshalBodyReturnsErrorForNilResponse 验证空响应不会触发 panic
func TestUnmarshalBodyReturnsErrorForNilResponse(t *testing.T) {
	client := NewHttpClient(context.Background()).CloseTracer()
	var out map[string]interface{}

	err := client.UnmarshalBody(nil, &out)
	require.ErrorContains(t, err, "response is nil")
}
