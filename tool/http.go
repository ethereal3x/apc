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

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"github.com/spf13/cast"
)

type HttpClient struct {
	Ctx          context.Context
	Method       string
	Url          string
	QueryParams  url.Values
	FormParams   url.Values
	Headers      http.Header
	Body         io.Reader
	Timeout      time.Duration
	Error        error
	EnableTrace  bool
	tracerName   string
	span         opentracing.Span
	spanFinished bool
}

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

// NewHttpClient 创建默认 HTTP 请求客户端
func NewHttpClient(ctx context.Context) *HttpClient {
	return &HttpClient{
		Ctx:         ctx,
		Timeout:     time.Second * 5,
		Headers:     make(http.Header),
		EnableTrace: true,
	}
}

// SetHeaders 设置请求头
func (c *HttpClient) SetHeaders(headers map[string]interface{}) *HttpClient {
	if len(headers) == 0 {
		c.Error = errors.New("headers is empty")
		return c
	}
	for k, v := range headers {
		c.Headers.Set(k, fmt.Sprintf("%v", v))
	}
	return c
}

// Request 执行 HTTP 请求并记录 tracing 信息
func (c *HttpClient) Request() (*http.Response, error) {
	c.startSpanFromContext()
	if c.Error != nil {
		c.logError("request_validation_error", c.Error)
		c.finishSpan()
		return nil, fmt.Errorf("http request validation failed: %w", c.Error)
	}

	startTime := time.Now()
	req, err := http.NewRequestWithContext(c.Ctx, c.Method, c.Url, c.Body)
	if err != nil {
		c.logError("request_validation_error", err)
		c.finishSpan()
		return nil, fmt.Errorf("create http request: %w", err)
	}
	if c.Headers != nil {
		req.Header = c.Headers.Clone()
	}

	httpClient := &http.Client{Timeout: c.Timeout}
	resp, err := httpClient.Do(req)
	if c.span == nil {
		if err != nil {
			return resp, fmt.Errorf("do http request: %w", err)
		}
		return resp, err
	}

	duration := time.Since(startTime)
	c.span.SetTag("http.duration_ms", duration.Milliseconds())
	if resp != nil {
		c.span.SetTag("http.status_code", resp.StatusCode)
		c.span.SetTag("http.response_size", resp.ContentLength)
	}
	if err != nil {
		c.logError("client_request_error", err)
		c.finishSpan()
		return resp, fmt.Errorf("do http request: %w", err)
	}
	if resp == nil || resp.Body == nil {
		c.finishSpan()
		return resp, nil
	}
	resp.Body = &tracingResponseBody{
		ReadCloser: resp.Body,
		finishSpan: c.finishSpan,
	}
	return resp, err
}

func (c *HttpClient) SetMethod(method string) *HttpClient {
	c.Method = method
	return c
}

// SetUrl 设置请求地址
func (c *HttpClient) SetUrl(rawURL string) *HttpClient {
	c.Url = rawURL
	return c
}

func (c *HttpClient) SetTimeout(timeout time.Duration) *HttpClient {
	c.Timeout = timeout
	return c
}

func (c *HttpClient) SetTracerName(name string) *HttpClient {
	c.tracerName = name
	return c
}

func (c *HttpClient) CloseTracer() *HttpClient {
	c.EnableTrace = false
	return c
}

func (c *HttpClient) SetQueryParams(params map[string]interface{}) *HttpClient {
	if len(params) == 0 {
		c.Error = errors.New("params is empty")
		return c
	}
	c.QueryParams = make(url.Values)
	for key, value := range params {
		c.QueryParams.Set(key, cast.ToString(value))
	}
	if c.Url == "" {
		c.Error = errors.New("URL must be set before adding query params")
		return c
	}
	pUrl, err := url.Parse(c.Url)
	if err != nil {
		c.Error = err
		return c
	}
	pUrl.RawQuery = c.QueryParams.Encode()
	c.Url = pUrl.String()
	return c
}

func (c *HttpClient) SetFormParams(params map[string]interface{}) *HttpClient {
	if len(params) == 0 {
		c.Error = errors.New("params is empty")
		return c
	}
	c.FormParams = make(url.Values)
	for key, value := range params {
		c.FormParams.Set(key, cast.ToString(value))
	}
	c.Body = strings.NewReader(c.FormParams.Encode())
	c.Headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return c
}

func (c *HttpClient) SetPathParams(params []string) *HttpClient {
	if len(params) == 0 {
		c.Error = errors.New("params is empty")
		return c
	}
	pUrl, err := url.Parse(c.Url)
	if err != nil {
		c.Error = err
		return c
	}
	pUrl.Path = path.Join(pUrl.Path, path.Join(params...))
	c.Url = pUrl.String()
	return c
}

func (c *HttpClient) SetBody(payload map[string]interface{}) *HttpClient {
	if len(payload) == 0 {
		c.Error = errors.New("body is empty")
		return c
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		c.Error = err
		return c
	}
	c.Body = bytes.NewBuffer(jsonBody)
	c.Headers.Set("Content-Type", "application/json")
	return c
}

// UnmarshalBody 解析响应 JSON 并关闭响应体
func (c *HttpClient) UnmarshalBody(resp *http.Response, out interface{}) (err error) {
	if resp == nil {
		err = errors.New("response is nil")
		c.logError("response_decode_error", err)
		return err
	}
	if resp.Body == nil {
		err = errors.New("response body is nil")
		c.logError("response_decode_error", err)
		return err
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("close response body: %w", closeErr)
			c.logError("response_close_error", err)
		}
	}()

	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		err = fmt.Errorf("decode response body: %w", err)
		c.logError("response_decode_error", err)
		return err
	}
	if c.span == nil {
		return nil
	}
	c.span.LogFields(log.Object("response_body", out))
	return nil
}

func (c *HttpClient) startSpanFromContext() {
	if !c.EnableTrace {
		return
	}
	if len(c.tracerName) == 0 {
		c.tracerName = fmt.Sprintf("%s %s", c.Method, c.Url)
	}
	c.span, c.Ctx = opentracing.StartSpanFromContext(c.Ctx, c.tracerName)
	c.spanFinished = false

	c.span.SetTag("http.method", c.Method)
	c.span.SetTag("http.url", c.Url)
	c.span.SetTag("component", "apc-http-client")

	c.span.LogFields(
		log.String("http.method", c.Method),
		log.String("http.url", c.Url),
	)
	// 只在有数据时记录
	if len(c.Headers) > 0 {
		c.span.LogFields(log.Object("http.headers", c.Headers))
	}
	if len(c.QueryParams) > 0 {
		c.span.LogFields(log.Object("http.query_params", c.QueryParams))
	}
	if len(c.FormParams) > 0 {
		c.span.LogFields(log.Object("http.form_data", c.FormParams))
	}
	if c.Body != nil {
		c.span.LogFields(log.Object("http.request_body", c.Body))
	}
}

// logError 记录 HTTP 请求链路错误
func (c *HttpClient) logError(errorType string, err error) {
	if c.span == nil || err == nil {
		return
	}
	c.span.SetTag("http.error_type", errorType)
	c.span.SetTag("error", true)
	c.span.LogFields(log.Error(err))
}

// finishSpan 结束 HTTP 请求 tracing span
func (c *HttpClient) finishSpan() {
	if c.span == nil || c.spanFinished {
		return
	}
	c.span.Finish()
	c.spanFinished = true
}
