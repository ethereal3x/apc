package tracing

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"strings"

	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

const defaultOTLPTraceURLPath = "/v1/traces"

var tracerName = "default_tracer"

// Config 定义 tracing 初始化配置
type Config struct {
	ServiceName string         `yaml:"service_name" json:"service_name"`
	Sampler     SamplerConfig  `yaml:"sampler" json:"sampler"`
	Reporter    ReporterConfig `yaml:"reporter" json:"reporter"`
}

// SamplerConfig 定义 tracing 采样配置
type SamplerConfig struct {
	Type  string  `yaml:"type" json:"type"`
	Param float64 `yaml:"param" json:"param"`
}

// AuthConfig 定义 OTLP reporter HTTP Basic 认证配置
type AuthConfig struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// ReporterConfig 定义 tracing 上报配置
type ReporterConfig struct {
	CollectorEndpoint string            `yaml:"collector_endpoint" json:"collector_endpoint"`
	URLPath           string            `yaml:"url_path" json:"url_path"`
	Insecure          bool              `yaml:"insecure" json:"insecure"`
	TLSSkipVerify     bool              `yaml:"tls_skip_verify" json:"tls_skip_verify"`
	Headers           map[string]string `yaml:"headers" json:"headers"`
	Auth              AuthConfig        `yaml:"auth" json:"auth"`
}

// otlpTraceHTTPSettings 描述 OTLP HTTP exporter 连接参数
type otlpTraceHTTPSettings struct {
	useFullURL bool
	fullURL    string
	endpoint   string
	urlPath    string
	insecure   bool
	headers    map[string]string
}

// InitProvider 根据配置初始化 tracing provider
func InitProvider(cfg Config) (func(ctx context.Context) error, error) {
	if cfg.Reporter.CollectorEndpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "default_tracer"
	}
	exporter, err := newOTLPTraceHTTPExporter(context.Background(), cfg.Reporter)
	if err != nil {
		return nil, fmt.Errorf("new otlp trace http exporter: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(buildSampler(cfg.Sampler)),
		sdktrace.WithResource(resource.NewSchemaless(
			semconv.ServiceNameKey.String(serviceName),
		)),
	)
	otel.SetTracerProvider(provider)
	tracerName = serviceName
	b3Propagator := b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader))
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}, b3Propagator,
	)
	otel.SetTextMapPropagator(propagator)
	return provider.Shutdown, nil
}

// newOTLPTraceHTTPExporter 创建 OTLP HTTP trace exporter
func newOTLPTraceHTTPExporter(ctx context.Context, reporter ReporterConfig) (sdktrace.SpanExporter, error) {
	options, err := buildOTLPTraceHTTPOptions(reporter)
	if err != nil {
		return nil, err
	}
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	return exporter, nil
}

// resolveReporterHeaders 合并自定义 headers，auth 配置优先生成 Authorization
func resolveReporterHeaders(cfg ReporterConfig) map[string]string {
	headers := make(map[string]string, len(cfg.Headers))
	for key, value := range cfg.Headers {
		headers[key] = value
	}
	if cfg.Auth.Username != "" && cfg.Auth.Password != "" {
		credentials := cfg.Auth.Username + ":" + cfg.Auth.Password
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
	}
	return headers
}

// isFullCollectorURL 判断 collector_endpoint 是否为带 scheme 的完整 URL
func isFullCollectorURL(endpoint string) bool {
	return strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://")
}

// resolveOTLPTraceHTTPSettings 解析 reporter 配置为 OTLP HTTP 连接参数
func resolveOTLPTraceHTTPSettings(cfg ReporterConfig) (otlpTraceHTTPSettings, error) {
	headers := resolveReporterHeaders(cfg)
	if isFullCollectorURL(cfg.CollectorEndpoint) {
		return otlpTraceHTTPSettings{
			useFullURL: true,
			fullURL:    cfg.CollectorEndpoint,
			insecure:   cfg.Insecure,
			headers:    headers,
		}, nil
	}
	urlPath := cfg.URLPath
	if urlPath == "" {
		urlPath = defaultOTLPTraceURLPath
	}
	return otlpTraceHTTPSettings{
		endpoint: cfg.CollectorEndpoint,
		urlPath:  urlPath,
		insecure: cfg.Insecure,
		headers:  headers,
	}, nil
}

// buildOTLPTraceHTTPOptions 构造 OTLP HTTP exporter 连接选项
func buildOTLPTraceHTTPOptions(cfg ReporterConfig) ([]otlptracehttp.Option, error) {
	settings, err := resolveOTLPTraceHTTPSettings(cfg)
	if err != nil {
		return nil, err
	}
	var options []otlptracehttp.Option
	if settings.useFullURL {
		options = append(options, otlptracehttp.WithEndpointURL(settings.fullURL))
		if settings.insecure {
			options = append(options, otlptracehttp.WithInsecure())
		}
	} else {
		options = append(options,
			otlptracehttp.WithEndpoint(settings.endpoint),
			otlptracehttp.WithURLPath(settings.urlPath),
		)
		if settings.insecure {
			options = append(options, otlptracehttp.WithInsecure())
		}
	}
	if len(settings.headers) > 0 {
		options = append(options, otlptracehttp.WithHeaders(settings.headers))
	}
	if cfg.TLSSkipVerify {
		options = append(options, otlptracehttp.WithTLSClientConfig(&tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // 自建 collector 自签证书场景
		}))
	}
	return options, nil
}

// buildSampler 根据配置创建 tracing 采样器
func buildSampler(cfg SamplerConfig) sdktrace.Sampler {
	switch cfg.Type {
	case "const":
		if cfg.Param <= 0 {
			return sdktrace.NeverSample()
		}
		return sdktrace.AlwaysSample()
	case "ratio":
		return sdktrace.TraceIDRatioBased(cfg.Param)
	default:
		return sdktrace.AlwaysSample()
	}
}

// Start 启动一个 span，始终从当前全局 tracer provider 获取 tracer
func Start(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name)
}

// TraceID 获取 TraceID
func TraceID(ctx context.Context) string {
	spanCtx := trace.SpanContextFromContext(ctx)
	return spanCtx.TraceID().String()
}

// SpanID 获取 SpanID
func SpanID(ctx context.Context) string {
	spanCtx := trace.SpanContextFromContext(ctx)
	return spanCtx.SpanID().String()
}

// RecordError 会将 error 记录到当前 Span，并设置 Span 状态为 Error
func RecordError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
