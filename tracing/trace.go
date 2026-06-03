package tracing

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/jaeger"
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
	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(cfg.Reporter.CollectorEndpoint)))
	if err != nil {
		return nil, fmt.Errorf("new jaeger exporter: %w", err)
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

// InitJaegerProvider 初始化 Jaeger 追踪器
func InitJaegerProvider(jaegerURL, serviceName string) (func(ctx context.Context) error, error) {
	if jaegerURL == "" {
		panic("empty jaeger url")
	}
	return InitProvider(Config{
		ServiceName: serviceName,
		Sampler: SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: ReporterConfig{CollectorEndpoint: jaegerURL},
	})
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

// Must 会捕获 panic 并转为错误上报到 tracing
func Must(ctx context.Context, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			var e error
			switch x := r.(type) {
			case error:
				e = x
			default:
				e = errors.New(fmt.Sprintf("%v", x))
			}
			RecordError(ctx, e)
			err = e
		}
	}()
	err = fn()
	if err != nil {
		RecordError(ctx, err)
	}
	return err
}
