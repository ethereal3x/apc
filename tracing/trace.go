package tracing

import (
	"context"
	"fmt"
	"net/url"

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

var tracer = otel.Tracer("default_tracer")

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

// ReporterConfig 定义 tracing 上报配置
type ReporterConfig struct {
	CollectorEndpoint string `yaml:"collector_endpoint" json:"collector_endpoint"`
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
	exporter, err := newOTLPTraceHTTPExporter(context.Background(), cfg.Reporter.CollectorEndpoint)
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
	tracer = otel.Tracer(serviceName)
	b3Propagator := b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader))
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}, b3Propagator,
	)
	otel.SetTextMapPropagator(propagator)
	return provider.Shutdown, nil
}

// newOTLPTraceHTTPExporter 创建 OTLP HTTP trace exporter
func newOTLPTraceHTTPExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	exporter, err := otlptracehttp.New(ctx, buildOTLPTraceHTTPOptions(endpoint)...)
	if err != nil {
		return nil, err
	}
	return exporter, nil
}

// buildOTLPTraceHTTPOptions 构造 OTLP HTTP exporter 连接选项
func buildOTLPTraceHTTPOptions(endpoint string) []otlptracehttp.Option {
	parsedURL, err := url.Parse(endpoint)
	if err == nil && parsedURL.Scheme != "" {
		return []otlptracehttp.Option{otlptracehttp.WithEndpointURL(endpoint)}
	}
	return []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	}
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

// Start 启动一个 span
func Start(ctx context.Context, name string) (context.Context, trace.Span) {
	return tracer.Start(ctx, name)
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
