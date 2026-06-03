package logger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/ethereal3x/apc/tracing"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LevelConfig 日志级别配置
type LevelConfig string

const (
	LevelDebug LevelConfig = "debug"
	LevelInfo  LevelConfig = "info"
	LevelWarn  LevelConfig = "warn"
	LevelError LevelConfig = "error"
	LevelFatal LevelConfig = "fatal"
	LevelPanic LevelConfig = "panic"
)

// FormatConfig 日志输出格式
type FormatConfig string

const (
	FormatJSON    FormatConfig = "json"
	FormatConsole FormatConfig = "console"
)

// Logger 定义日志组件对外暴露的基础行为
type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
	Fatal(msg string, fields ...zap.Field)
	Panic(msg string, fields ...zap.Field)
	ContextDebug(ctx context.Context, msg string, fields ...zap.Field)
	ContextInfo(ctx context.Context, msg string, fields ...zap.Field)
	ContextWarn(ctx context.Context, msg string, fields ...zap.Field)
	ContextError(ctx context.Context, msg string, fields ...zap.Field)
	ContextPanic(ctx context.Context, msg string, fields ...zap.Field)
	Sync() error
}

// ZapLogger 基于 zap 实现 Logger 接口
type ZapLogger struct {
	logger *zap.Logger
}

type callerSkipLogger interface {
	withCallerSkip(skip int) Logger
}

var activeLogger Logger

// Config 日志配置
type Config struct {
	Level      LevelConfig  `mapstructure:"level" json:"level" yaml:"level"`
	Format     FormatConfig `mapstructure:"format" json:"format" yaml:"format"`
	OutputPath string       `mapstructure:"output_path" json:"logfile" yaml:"logfile"`
}

// NewLogger 创建日志实例
func NewLogger(logCfg ...*Config) Logger {
	cfg := defaultConfig()
	if len(logCfg) > 0 && logCfg[0] != nil {
		cfg = fillDefaultConfig(*logCfg[0])
	}
	newLogger, err := NewZapLogger(cfg)
	if err != nil {
		panic(fmt.Errorf("new logger: %w", err))
	}
	return newLogger
}

// defaultConfig 返回日志默认配置
func defaultConfig() Config {
	return Config{
		Level:  LevelInfo,
		Format: FormatConsole,
	}
}

// fillDefaultConfig 使用默认值补齐日志配置
func fillDefaultConfig(cfg Config) Config {
	defaultCfg := defaultConfig()
	if cfg.Level == "" {
		cfg.Level = defaultCfg.Level
	}
	if cfg.Format == "" {
		cfg.Format = defaultCfg.Format
	}
	return cfg
}

// NewZapLogger 根据配置创建 zap 日志实例
func NewZapLogger(cfg Config) (*ZapLogger, error) {
	// 解析日志级别
	level := parseLevel(cfg.Level)
	// 构建日志编码器
	encoder := buildEncoder(cfg)
	// 构建日志输出目标
	writeSyncer, err := buildWriteSyncer(cfg.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("build write syncer: %w", err)
	}
	core := zapcore.NewCore(encoder, writeSyncer, level)
	return &ZapLogger{logger: zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))}, nil
}

// SetLogger 设置默认日志实例
func SetLogger(newLogger Logger) {
	activeLogger = newLogger
}

// Sync 同步默认日志实例的缓冲内容
func Sync() error {
	if activeLogger == nil {
		return nil
	}
	if err := activeLogger.Sync(); err != nil {
		return fmt.Errorf("sync active logger: %w", err)
	}
	return nil
}

// L 返回默认日志实例
func L() Logger {
	if activeLogger == nil {
		panic("logger not initialized")
	}
	return activeLogger
}

// Debug 记录默认日志实例的 debug 日志
func Debug(msg string, fields ...zap.Field) { withCallerSkip(L(), 1).Debug(msg, fields...) }

// Info 记录默认日志实例的 info 日志
func Info(msg string, fields ...zap.Field) { withCallerSkip(L(), 1).Info(msg, fields...) }

// Warn 记录默认日志实例的 warn 日志
func Warn(msg string, fields ...zap.Field) { withCallerSkip(L(), 1).Warn(msg, fields...) }

// Error 记录默认日志实例的 error 日志
func Error(msg string, fields ...zap.Field) { withCallerSkip(L(), 1).Error(msg, fields...) }

// Fatal 记录默认日志实例的 fatal 日志
func Fatal(msg string, fields ...zap.Field) { withCallerSkip(L(), 1).Fatal(msg, fields...) }

// Panic 记录默认日志实例的 panic 日志
func Panic(msg string, fields ...zap.Field) { withCallerSkip(L(), 1).Panic(msg, fields...) }

// withCallerSkip 返回带有调用栈跳过层数的日志实例
func withCallerSkip(currentLogger Logger, skip int) Logger {
	skipLogger, ok := currentLogger.(callerSkipLogger)
	if !ok {
		return currentLogger
	}
	return skipLogger.withCallerSkip(skip)
}

// Debug 记录 zap logger 的 debug 日志
func (zapLogger *ZapLogger) Debug(msg string, fields ...zap.Field) {
	zapLogger.logger.Debug(msg, fields...)
}

// Info 记录 zap logger 的 info 日志
func (zapLogger *ZapLogger) Info(msg string, fields ...zap.Field) {
	zapLogger.logger.Info(msg, fields...)
}

// Warn 记录 zap logger 的 warn 日志
func (zapLogger *ZapLogger) Warn(msg string, fields ...zap.Field) {
	zapLogger.logger.Warn(msg, fields...)
}

// Error 记录 zap logger 的 error 日志
func (zapLogger *ZapLogger) Error(msg string, fields ...zap.Field) {
	zapLogger.logger.Error(msg, fields...)
}

// Fatal 记录 zap logger 的 fatal 日志
func (zapLogger *ZapLogger) Fatal(msg string, fields ...zap.Field) {
	zapLogger.logger.Fatal(msg, fields...)
}

// Panic 记录 zap logger 的 panic 日志
func (zapLogger *ZapLogger) Panic(msg string, fields ...zap.Field) {
	zapLogger.logger.Panic(msg, fields...)
}

// Sync 同步 zap logger 的缓冲内容
func (zapLogger *ZapLogger) Sync() error {
	if err := zapLogger.logger.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.EBADF) {
		return fmt.Errorf("sync zap logger: %w", err)
	}
	return nil
}

// withCallerSkip 返回调整调用栈层级后的 zap logger
func (zapLogger *ZapLogger) withCallerSkip(skip int) Logger {
	return &ZapLogger{logger: zapLogger.logger.WithOptions(zap.AddCallerSkip(skip))}
}

type ctxKey string

const (
	ctxTraceID   ctxKey = "trace_id"
	ctxSpanID    ctxKey = "span_id"
	emptyTraceID        = "00000000000000000000000000000000"
	emptySpanID         = "0000000000000000"
)

// WithTraceID 写入链路追踪 ID
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxTraceID, traceID)
}

// WithSpanID 写入链路 span ID
func WithSpanID(ctx context.Context, spanID string) context.Context {
	return context.WithValue(ctx, ctxSpanID, spanID)
}

// ContextDebug 记录携带上下文字段的 debug 日志
func ContextDebug(ctx context.Context, msg string, fields ...zap.Field) {
	withCallerSkip(L(), 1).ContextDebug(ctx, msg, fields...)
}

// ContextInfo 记录携带上下文字段的 info 日志
func ContextInfo(ctx context.Context, msg string, fields ...zap.Field) {
	withCallerSkip(L(), 1).ContextInfo(ctx, msg, fields...)
}

// ContextWarn 记录携带上下文字段的 warn 日志
func ContextWarn(ctx context.Context, msg string, fields ...zap.Field) {
	withCallerSkip(L(), 1).ContextWarn(ctx, msg, fields...)
}

// ContextError 记录携带上下文字段的 error 日志
func ContextError(ctx context.Context, msg string, fields ...zap.Field) {
	withCallerSkip(L(), 1).ContextError(ctx, msg, fields...)
}

// ContextDebug 记录 zap logger 携带上下文字段的 debug 日志
func (zapLogger *ZapLogger) ContextDebug(ctx context.Context, msg string, fields ...zap.Field) {
	zapLogger.logger.Debug(msg, append(extractCtxFields(ctx), fields...)...)
}

// ContextInfo 记录 zap logger 携带上下文字段的 info 日志
func (zapLogger *ZapLogger) ContextInfo(ctx context.Context, msg string, fields ...zap.Field) {
	zapLogger.logger.Info(msg, append(extractCtxFields(ctx), fields...)...)
}

// ContextWarn 记录 zap logger 携带上下文字段的 warn 日志
func (zapLogger *ZapLogger) ContextWarn(ctx context.Context, msg string, fields ...zap.Field) {
	zapLogger.logger.Warn(msg, append(extractCtxFields(ctx), fields...)...)
}

// ContextError 记录 zap logger 携带上下文字段的 error 日志
func (zapLogger *ZapLogger) ContextError(ctx context.Context, msg string, fields ...zap.Field) {
	zapLogger.logger.Error(msg, append(extractCtxFields(ctx), fields...)...)
}

func (zapLogger *ZapLogger) ContextPanic(ctx context.Context, msg string, fields ...zap.Field) {
	zapLogger.logger.Fatal(msg, append(extractCtxFields(ctx), fields...)...)
}

// parseLevel 将日志级别配置转换为 zap 级别
func parseLevel(level LevelConfig) zapcore.Level {
	switch level {
	case LevelDebug:
		return zap.DebugLevel
	case LevelWarn:
		return zap.WarnLevel
	case LevelError:
		return zap.ErrorLevel
	case LevelFatal:
		return zap.FatalLevel
	case LevelPanic:
		return zap.PanicLevel
	default:
		return zap.InfoLevel
	}
}

// buildEncoder 根据配置创建日志编码器
func buildEncoder(cfg Config) zapcore.Encoder {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "time"
	encoderCfg.LevelKey = "level"
	encoderCfg.NameKey = "logger"
	encoderCfg.CallerKey = "caller"
	encoderCfg.MessageKey = "msg"
	encoderCfg.StacktraceKey = "stack"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeCaller = zapcore.ShortCallerEncoder
	if cfg.Format == FormatConsole && cfg.OutputPath == "" {
		encoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encoderCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	}
	if cfg.Format == FormatJSON {
		return zapcore.NewJSONEncoder(encoderCfg)
	}
	return zapcore.NewConsoleEncoder(encoderCfg)
}

// buildWriteSyncer 根据输出路径创建日志输出目标
func buildWriteSyncer(outputPath string) (zapcore.WriteSyncer, error) {
	writeSyncer := zapcore.AddSync(os.Stdout)
	if outputPath == "" {
		return writeSyncer, nil
	}
	file, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", outputPath, err)
	}
	return zapcore.NewMultiWriteSyncer(writeSyncer, zapcore.AddSync(file)), nil
}

// extractCtxFields 提取上下文中的日志字段
func extractCtxFields(ctx context.Context) []zap.Field {
	fields := make([]zap.Field, 0, 2)
	traceID := tracing.TraceID(ctx)
	if traceID == "" || traceID == emptyTraceID {
		if manualTraceID, ok := ctx.Value(ctxTraceID).(string); ok {
			traceID = manualTraceID
		}
	}
	if traceID != "" && traceID != emptyTraceID {
		fields = append(fields, zap.String("trace_id", traceID))
	}
	spanID := tracing.SpanID(ctx)
	if spanID == "" || spanID == emptySpanID {
		if manualSpanID, ok := ctx.Value(ctxSpanID).(string); ok {
			spanID = manualSpanID
		}
	}
	if spanID != "" && spanID != emptySpanID {
		fields = append(fields, zap.String("span_id", spanID))
	}
	return fields
}
