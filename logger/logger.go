package logger

import (
	"context"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
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

var logger *zap.Logger

// Config 日志配置
type Config struct {
	Level      LevelConfig
	Format     FormatConfig
	OutputPath string
}

func LogInit(cfg Config) {
	var level zapcore.Level
	switch cfg.Level {
	case LevelDebug:
		level = zap.DebugLevel
	case LevelWarn:
		level = zap.WarnLevel
	case LevelError:
		level = zap.ErrorLevel
	case LevelFatal:
		level = zap.FatalLevel
	case LevelInfo:
		level = zap.InfoLevel
	case LevelPanic:
		level = zap.PanicLevel
	}
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "time"
	encoderCfg.LevelKey = "level"
	encoderCfg.NameKey = "logger"
	encoderCfg.CallerKey = "caller"
	encoderCfg.MessageKey = "msg"
	encoderCfg.StacktraceKey = "stack"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeCaller = zapcore.ShortCallerEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	}

	if cfg.Format == "console" && cfg.OutputPath == "" {
		encoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encoderCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	}

	writeSyncer := zapcore.AddSync(os.Stdout)
	if cfg.OutputPath != "" {
		file, _ := os.OpenFile(cfg.OutputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		writeSyncer = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(file))
	}

	core := zapcore.NewCore(encoder, writeSyncer, level)
	logger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}

func Sync() {
	if logger != nil {
		_ = logger.Sync()
	}
}

func L() *zap.Logger {
	if logger == nil {
		panic("logger not initialized")
	}
	return logger
}

func Debug(msg string, fields ...zap.Field) { L().Debug(msg, fields...) }
func Info(msg string, fields ...zap.Field)  { L().Info(msg, fields...) }
func Warn(msg string, fields ...zap.Field)  { L().Warn(msg, fields...) }
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }
func Fatal(msg string, fields ...zap.Field) { L().Fatal(msg, fields...) }
func Panic(msg string, fields ...zap.Field) { L().Panic(msg, fields...) }

type ctxKey string

const (
	ctxTraceID ctxKey = "trace_id"
	ctxSpanID  ctxKey = "span_id"
)

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxTraceID, traceID)
}

func WithSpanID(ctx context.Context, spanID string) context.Context {
	return context.WithValue(ctx, ctxSpanID, spanID)
}

func extractCtxFields(ctx context.Context) []zap.Field {
	var fs []zap.Field
	if v := ctx.Value(ctxTraceID); v != nil {
		fs = append(fs, zap.String("trace_id", v.(string)))
	}
	if v := ctx.Value(ctxSpanID); v != nil {
		fs = append(fs, zap.String("span_id", v.(string)))
	}
	return fs
}

func ContextDebug(ctx context.Context, msg string, fields ...zap.Field) {
	L().Debug(msg, append(extractCtxFields(ctx), fields...)...)
}
func ContextInfo(ctx context.Context, msg string, fields ...zap.Field) {
	L().Info(msg, append(extractCtxFields(ctx), fields...)...)
}
func ContextWarn(ctx context.Context, msg string, fields ...zap.Field) {
	L().Warn(msg, append(extractCtxFields(ctx), fields...)...)
}
func ContextError(ctx context.Context, msg string, fields ...zap.Field) {
	L().Error(msg, append(extractCtxFields(ctx), fields...)...)
}
