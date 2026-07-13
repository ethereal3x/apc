package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	apctracing "github.com/ethereal3x/apc/tracing"
	"github.com/spf13/cast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ErrParamsEmpty 参数为空
var ErrParamsEmpty = errors.New("params is empty")

// HttpClient 构建并执行单次 HTTP 请求，支持链式调用
type HttpClient struct {
	Ctx         context.Context
	Method      string
	Url         string
	QueryParams url.Values
	FormParams  url.Values
	Headers     http.Header
	Body        io.Reader
	Timeout     time.Duration
	Error       error
	EnableTrace bool

	httpClient        *http.Client
	timeoutConfigured bool
	tracerName        string
	span              trace.Span
	spanFinished      bool
}

// HttpClientOption 客户端配置选项
type HttpClientOption func(*HttpClient)

// WithHTTPClient 注入底层 http.Client，便于测试和自定义 Transport
func WithHTTPClient(httpClient *http.Client) HttpClientOption {
	return func(client *HttpClient) {
		if httpClient == nil {
			client.Error = errors.New("http client is nil")
			return
		}
		client.httpClient = httpClient
		if !client.timeoutConfigured {
			client.Timeout = httpClient.Timeout
		}
	}
}

// WithTimeout 设置请求超时时间
func WithTimeout(timeout time.Duration) HttpClientOption {
	return func(client *HttpClient) {
		client.Timeout = timeout
		client.timeoutConfigured = true
	}
}

// WithEnableTrace 设置是否启用 tracing
func WithEnableTrace(enable bool) HttpClientOption {
	return func(client *HttpClient) { client.EnableTrace = enable }
}

// NewHttpClient 创建 HTTP 请求客户端，默认超时 5s 并启用 tracing
func NewHttpClient(ctx context.Context, opts ...HttpClientOption) *HttpClient {
	client := &HttpClient{
		Ctx:         ctx,
		httpClient:  http.DefaultClient,
		Timeout:     5 * time.Second,
		Headers:     make(http.Header),
		EnableTrace: true,
	}
	for _, option := range opts {
		option(client)
	}
	return client
}

// SetMethod 设置请求方法
func (client *HttpClient) SetMethod(method string) *HttpClient {
	client.Method = method
	return client
}

// SetUrl 设置请求地址
func (client *HttpClient) SetUrl(rawURL string) *HttpClient {
	client.Url = rawURL
	return client
}

// SetHeaders 设置请求头
func (client *HttpClient) SetHeaders(headers map[string]interface{}) *HttpClient {
	for key, value := range headers {
		client.Headers.Set(key, cast.ToString(value))
	}
	return client
}

// SetTimeout 设置请求超时时间，覆盖默认值
func (client *HttpClient) SetTimeout(timeout time.Duration) *HttpClient {
	client.Timeout = timeout
	client.timeoutConfigured = true
	return client
}

// SetTracerName 设置 tracer 名称
func (client *HttpClient) SetTracerName(name string) *HttpClient {
	client.tracerName = name
	return client
}

// CloseTracer 禁用 tracing
func (client *HttpClient) CloseTracer() *HttpClient {
	client.EnableTrace = false
	return client
}

// SetQueryParams 设置查询参数并追加到 URL
func (client *HttpClient) SetQueryParams(params map[string]interface{}) *HttpClient {
	if len(params) == 0 {
		client.Error = ErrParamsEmpty
		return client
	}
	client.QueryParams = make(url.Values)
	for key, value := range params {
		client.QueryParams.Set(key, cast.ToString(value))
	}
	if client.Url == "" {
		client.Error = errors.New("URL must be set before adding query params")
		return client
	}
	parsedURL, err := url.Parse(client.Url)
	if err != nil {
		client.Error = err
		return client
	}
	parsedURL.RawQuery = client.QueryParams.Encode()
	client.Url = parsedURL.String()
	return client
}

// SetFormParams 设置表单参数
func (client *HttpClient) SetFormParams(params map[string]interface{}) *HttpClient {
	if len(params) == 0 {
		client.Error = ErrParamsEmpty
		return client
	}
	client.FormParams = make(url.Values)
	for key, value := range params {
		client.FormParams.Set(key, cast.ToString(value))
	}
	client.Body = strings.NewReader(client.FormParams.Encode())
	client.Headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return client
}

// SetPathParams 追加路径参数到 URL
func (client *HttpClient) SetPathParams(params []string) *HttpClient {
	if len(params) == 0 {
		client.Error = ErrParamsEmpty
		return client
	}
	parsedURL, err := url.Parse(client.Url)
	if err != nil {
		client.Error = err
		return client
	}
	parsedURL.Path = path.Join(parsedURL.Path, path.Join(params...))
	client.Url = parsedURL.String()
	return client
}

// SetBody 设置 JSON 请求体
func (client *HttpClient) SetBody(payload map[string]interface{}) *HttpClient {
	if len(payload) == 0 {
		client.Error = ErrParamsEmpty
		return client
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		client.Error = err
		return client
	}
	client.Body = bytes.NewBuffer(jsonBody)
	client.Headers.Set("Content-Type", "application/json")
	return client
}

// SetRawBody 设置原始请求体，供调用方传入自定义 io.Reader
func (client *HttpClient) SetRawBody(body io.Reader) *HttpClient {
	client.Body = body
	return client
}

// Do 校验并执行 HTTP 请求，记录 tracing 信息
func (client *HttpClient) Do() (*http.Response, error) {
	client.startSpanFromContext()
	if client.Error != nil {
		client.logError("request_validation_error", client.Error)
		client.finishSpan()
		return nil, fmt.Errorf("http request validation failed: %w", client.Error)
	}
	if client.Method == "" {
		err := errors.New("http method is empty")
		client.logError("request_validation_error", err)
		client.finishSpan()
		return nil, err
	}
	if client.Url == "" {
		err := errors.New("http url is empty")
		client.logError("request_validation_error", err)
		client.finishSpan()
		return nil, err
	}

	startTime := time.Now()
	request, err := http.NewRequestWithContext(client.Ctx, client.Method, client.Url, client.Body)
	if err != nil {
		client.logError("request_validation_error", err)
		client.finishSpan()
		return nil, fmt.Errorf("create http request: %w", err)
	}
	request.Header = client.Headers.Clone()

	requestClient := *client.httpClient
	requestClient.Timeout = client.Timeout
	response, err := requestClient.Do(request)
	if client.span == nil {
		if err != nil {
			return response, fmt.Errorf("do http request: %w", err)
		}
		return response, nil
	}

	duration := time.Since(startTime)
	client.span.SetAttributes(attribute.Int64("http.duration_ms", duration.Milliseconds()))
	if response != nil {
		client.span.SetAttributes(
			attribute.Int("http.status_code", response.StatusCode),
			attribute.Int64("http.response_size", response.ContentLength),
		)
	}
	if err != nil {
		client.logError("client_request_error", err)
		client.finishSpan()
		return response, fmt.Errorf("do http request: %w", err)
	}
	if response == nil || response.Body == nil {
		client.finishSpan()
		return response, nil
	}
	response.Body = &tracingResponseBody{
		ReadCloser: response.Body,
		finishSpan: client.finishSpan,
	}
	return response, nil
}

// Request 执行 HTTP 请求，等价于 Do，保留向后兼容
func (client *HttpClient) Request() (*http.Response, error) {
	return client.Do()
}

// UnmarshalBody 解析响应 JSON 并关闭响应体
func (client *HttpClient) UnmarshalBody(response *http.Response, out interface{}) (err error) {
	if response == nil {
		err = errors.New("response is nil")
		client.logError("response_decode_error", err)
		return err
	}
	if response.Body == nil {
		err = errors.New("response body is nil")
		client.logError("response_decode_error", err)
		return err
	}
	defer func() {
		closeErr := response.Body.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("close response body: %w", closeErr)
			client.logError("response_close_error", err)
		}
	}()

	if err = json.NewDecoder(response.Body).Decode(out); err != nil {
		err = fmt.Errorf("decode response body: %w", err)
		client.logError("response_decode_error", err)
		return err
	}
	if client.span == nil {
		return nil
	}
	responseJSON, marshalErr := json.Marshal(out)
	if marshalErr == nil {
		client.span.AddEvent("response_body", trace.WithAttributes(attribute.String("body", string(responseJSON))))
	}
	return nil
}

// tracingResponseBody 包装响应体，在 Close 时结束 tracing span
type tracingResponseBody struct {
	io.ReadCloser
	finishSpan func()
}

// Close 关闭响应体并结束请求 tracing span
func (body *tracingResponseBody) Close() error {
	err := body.ReadCloser.Close()
	body.finishSpan()
	if err != nil {
		return fmt.Errorf("close response body: %w", err)
	}
	return nil
}

// startSpanFromContext 从 context 创建 HTTP 请求 tracing span 并设置属性
func (client *HttpClient) startSpanFromContext() {
	if !client.EnableTrace {
		return
	}
	tracerName := client.tracerName
	if len(tracerName) == 0 {
		tracerName = fmt.Sprintf("%s %s", client.Method, client.Url)
	}
	client.Ctx, client.span = apctracing.Start(client.Ctx, tracerName)
	client.spanFinished = false

	client.span.SetAttributes(
		attribute.String("http.method", client.Method),
		attribute.String("http.url", client.Url),
		attribute.String("component", "apc-http-client"),
	)
	if len(client.Headers) > 0 {
		client.span.AddEvent("http.headers", trace.WithAttributes(attribute.String("headers", fmt.Sprintf("%v", client.Headers))))
	}
	if len(client.QueryParams) > 0 {
		client.span.AddEvent("http.query_params", trace.WithAttributes(attribute.String("params", fmt.Sprintf("%v", client.QueryParams))))
	}
	if len(client.FormParams) > 0 {
		client.span.AddEvent("http.form_data", trace.WithAttributes(attribute.String("params", fmt.Sprintf("%v", client.FormParams))))
	}
	if client.Body != nil {
		client.span.AddEvent("http.request_body", trace.WithAttributes(attribute.String("body", fmt.Sprintf("%v", client.Body))))
	}
}

// logError 记录 HTTP 请求链路错误
func (client *HttpClient) logError(errorType string, err error) {
	if client.span == nil || err == nil {
		return
	}
	client.span.SetAttributes(
		attribute.String("http.error_type", errorType),
		attribute.Bool("error", true),
	)
	client.span.SetStatus(codes.Error, err.Error())
	client.span.RecordError(err)
}

// finishSpan 结束 HTTP 请求 tracing span
func (client *HttpClient) finishSpan() {
	if client.span == nil || client.spanFinished {
		return
	}
	client.span.End()
	client.spanFinished = true
}
