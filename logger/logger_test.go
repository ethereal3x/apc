package logger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereal3x/apc/tracing"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestLoggerWithOTLPTrace 验证 logger 搭配 OTLP tracing 记录正常调用链
func TestLoggerWithOTLPTrace(t *testing.T) {
	shutdown := initTestOTLP(t)
	defer shutdownTestOTLP(t, shutdown)
	logPath := filepath.Join("./", "otlp.log")
	SetLogger(NewLogger(&Config{
		Level:      LevelInfo,
		Format:     FormatJSON,
		OutputPath: logPath,
	}))
	ctx, span := tracing.Start(context.Background(), "otlp-root")
	defer span.End()

	records, err := traceMethodA(ctx, false)
	require.NoError(t, err)
	require.NoError(t, Sync())
	assertTracingRecords(t, records)
	assertLogContains(t, logPath, records[0].traceID)
	t.Logf("otlp trace_id=%s", records[0].traceID)
}

// TestLoggerWithOTLPError 验证 logger 搭配 OTLP tracing 记录错误调用链
func TestLoggerWithOTLPError(t *testing.T) {
	shutdown := initTestOTLP(t)
	defer shutdownTestOTLP(t, shutdown)
	logPath := filepath.Join("./", "otlp-error.log")
	SetLogger(NewLogger(&Config{
		Level:      LevelInfo,
		Format:     FormatJSON,
		OutputPath: logPath,
	}))
	ctx, span := tracing.Start(context.Background(), "otlp-error-root")
	defer span.End()

	records, err := traceMethodA(ctx, true)
	require.Error(t, err)
	require.NoError(t, Sync())
	assertTracingRecords(t, records)
	assertLogContains(t, logPath, err.Error())
	t.Logf("otlp error trace_id=%s error=%s", records[0].traceID, err.Error())
}

type tracingRecord struct {
	methodName string
	traceID    string
	spanID     string
}

// traceMethodA 模拟 A 方法调用 B 方法
func traceMethodA(ctx context.Context, shouldFail bool) ([]tracingRecord, error) {
	ctx, span := tracing.Start(ctx, "method-a")
	defer span.End()
	ContextInfo(ctx, "调用 A 方法")
	records := []tracingRecord{newTracingRecord("A", ctx)}
	childRecords, err := traceMethodB(ctx, shouldFail)
	if err != nil {
		tracing.RecordError(ctx, err)
		ContextError(ctx, "A 方法处理失败", zap.Error(err))
	}
	return append(records, childRecords...), err
}

// traceMethodB 模拟 B 方法调用 C 方法
func traceMethodB(ctx context.Context, shouldFail bool) ([]tracingRecord, error) {
	ctx, span := tracing.Start(ctx, "method-b")
	defer span.End()
	ContextInfo(ctx, "调用 B 方法")
	records := []tracingRecord{newTracingRecord("B", ctx)}
	childRecords, err := traceMethodC(ctx, shouldFail)
	if err != nil {
		tracing.RecordError(ctx, err)
		ContextError(ctx, "B 方法处理失败", zap.Error(err))
	}
	return append(records, childRecords...), err
}

// traceMethodC 模拟 C 方法处理成功或返回错误
func traceMethodC(ctx context.Context, shouldFail bool) ([]tracingRecord, error) {
	ctx, span := tracing.Start(ctx, "method-c")
	defer span.End()
	records := []tracingRecord{newTracingRecord("C", ctx)}
	if !shouldFail {
		ContextInfo(ctx, "调用 C 方法")
		return records, nil
	}
	err := errors.New("模拟 C 方法业务错误")
	tracing.RecordError(ctx, err)
	ContextError(ctx, "C 方法处理失败", zap.Error(err))
	return records, err
}

// newTracingRecord 记录当前方法的 tracing 标识
func newTracingRecord(methodName string, ctx context.Context) tracingRecord {
	return tracingRecord{
		methodName: methodName,
		traceID:    tracing.TraceID(ctx),
		spanID:     tracing.SpanID(ctx),
	}
}

// initTestOTLP 初始化测试用 OTLP provider
func initTestOTLP(t *testing.T) func(context.Context) error {
	t.Helper()
	shutdown, err := tracing.InitProvider(tracing.Config{
		ServiceName: "apc-logger-test",
		Sampler: tracing.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: tracing.ReporterConfig{CollectorEndpoint: "http://localhost:4318/v1/traces"},
	})
	require.NoError(t, err)
	return shutdown
}

// shutdownTestOTLP 关闭测试用 OTLP provider
func shutdownTestOTLP(t *testing.T, shutdown func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, shutdown(ctx))
}

// assertTracingRecords 校验调用链 tracing 标识
func assertTracingRecords(t *testing.T, records []tracingRecord) {
	t.Helper()
	require.Len(t, records, 3)
	for _, record := range records {
		t.Logf("method=%s trace_id=%s span_id=%s", record.methodName, record.traceID, record.spanID)
	}
	require.Equal(t, records[0].traceID, records[1].traceID)
	require.Equal(t, records[0].traceID, records[2].traceID)
	require.NotEqual(t, records[0].spanID, records[1].spanID)
	require.NotEqual(t, records[1].spanID, records[2].spanID)
	require.NotEqual(t, records[0].spanID, records[2].spanID)
}

// assertLogContains 校验日志文件包含指定内容
func assertLogContains(t *testing.T, logPath string, content string) {
	t.Helper()
	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(logData), content)
}
