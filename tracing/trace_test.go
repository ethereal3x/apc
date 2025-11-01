package tracing

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
)

func TestInitJaegerProvider(t *testing.T) {
	// 使用本地 Jaeger Collector 地址
	shutdown, err := InitJaegerProvider("http://localhost:14268/api/traces", "test-service")
	if err != nil {
		t.Fatalf("初始化 Jaeger Provider 失败: %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("关闭 tracer provider 失败: %v", err)
		}
	}()
	// 检查全局 tracer provider 是否设置
	tp := otel.GetTracerProvider()
	if tp == nil {
		t.Fatal("全局 TracerProvider 未设置")
	}
	// 创建第一个 span
	ctx := context.Background()
	ctx, span := Start(ctx, "root-operation")
	if span == nil {
		t.Fatal("未创建 span")
	}
	defer span.End()
	traceID := TraceID(ctx)
	if traceID == "" {
		t.Error("TraceID 不应为空")
	} else {
		t.Logf("生成的 TraceID: %s", traceID)
	}
	// 创建嵌套 span
	ctx2, subSpan := Start(ctx, "child-operation")
	time.Sleep(50 * time.Millisecond)
	subSpan.End()
	// 检查 TraceID 一致（父子 span 同一个 trace）
	traceID2 := TraceID(ctx2)
	if traceID != traceID2 {
		t.Errorf("TraceID 不一致: root=%s child=%s", traceID, traceID2)
	}
	// 检查全局 tracer 是否为 otel 默认
	if otel.GetTracerProvider() == nil {
		t.Error("otel 全局 tracer 未初始化")
	}
	t.Log("tracing 初始化、span 嵌套、TraceID 校验均通过")
}

func TracingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// 从 ctx 中创建 span
		ctx, span := Start(ctx, info.FullMethod)
		defer span.End()

		// 调用实际处理函数
		resp, err := handler(ctx, req)
		if err != nil {
			RecordError(ctx, err)
		}
		return resp, err
	}
	/**
	server := grpc.NewServer(
		grpc.UnaryInterceptor(TracingUnaryInterceptor()),
	)
	*/
}
