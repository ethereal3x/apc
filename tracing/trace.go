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

// InitJaegerProvider 初始化 Jaeger 追踪器
func InitJaegerProvider(jaegerURL, serviceName string) (func(ctx context.Context) error, error) {
	if jaegerURL == "" {
		panic("empty jaeger url")
	}

	tracer = otel.Tracer(serviceName)

	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerURL)))
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewSchemaless(
			semconv.ServiceNameKey.String(serviceName),
		)),
	)
	otel.SetTracerProvider(tp)

	b3Propagator := b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader))
	p := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}, b3Propagator,
	)
	otel.SetTextMapPropagator(p)

	return tp.Shutdown, nil
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
