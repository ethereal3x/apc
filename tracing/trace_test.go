package tracing

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestInitProvider(t *testing.T) {
	// 使用本地 OTLP HTTP Collector 地址
	shutdown, err := InitProvider(Config{
		ServiceName: "test-service",
		Sampler: SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: ReporterConfig{CollectorEndpoint: "http://localhost:4318/v1/traces"},
	})
	if err != nil {
		t.Fatalf("初始化 OTLP Provider 失败: %v", err)
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
