package orm

import (
	"context"
	"errors"
	"fmt"
	"time"

	apcLogger "github.com/ethereal3x/apc/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

type GormLogger struct {
	LogLevel logger.LogLevel
}

// NewGormDBLog 创建 Gorm 日志适配器
func NewGormDBLog() *GormLogger {
	return &GormLogger{}
}

// LogMode 设置 Gorm 日志级别
func (gormLogger *GormLogger) LogMode(level logger.LogLevel) logger.Interface {
	return &GormLogger{LogLevel: level}
}

// Info 输出 Gorm info 日志
func (gormLogger *GormLogger) Info(ctx context.Context, message string, data ...interface{}) {
	if gormLogger.LogLevel < logger.Info {
		return
	}
	apcLogger.ContextInfo(ctx, message, zap.Any("data", data))
}

// Warn 输出 Gorm warn 日志
func (gormLogger *GormLogger) Warn(ctx context.Context, message string, data ...interface{}) {
	if gormLogger.LogLevel < logger.Warn {
		return
	}
	apcLogger.ContextWarn(ctx, message, zap.Any("data", data))
}

// Error 输出 Gorm error 日志
func (gormLogger *GormLogger) Error(ctx context.Context, message string, data ...interface{}) {
	if gormLogger.LogLevel < logger.Error {
		return
	}
	apcLogger.ContextError(ctx, message, zap.Any("data", data))
}

// Trace 输出 Gorm SQL 执行日志
func (gormLogger *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if gormLogger.LogLevel <= logger.Silent {
		return
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		if gormLogger.LogLevel >= logger.Error {
			sql, rows := fc()
			apcLogger.ContextError(ctx, "sql_exec_error", zap.Any("sql", sql), zap.Any("rows", rows),
				zap.Any("stack_file", utils.FileWithLineNum()), zap.Any("elapsed_time", formatElapsedTime(begin)), zap.Any("err", err))
		}
		return
	}
	if gormLogger.LogLevel < logger.Info {
		return
	}
	sql, rows := fc()
	apcLogger.ContextInfo(ctx, "sql_exec_info", zap.Any("sql", sql), zap.Any("rows", rows),
		zap.Any("stack_file", utils.FileWithLineNum()), zap.Any("elapsed_time", formatElapsedTime(begin)), zap.Any("err", err))
}

// formatElapsedTime 格式化 SQL 执行耗时
func formatElapsedTime(begin time.Time) string {
	elapsed := time.Since(begin)
	return fmt.Sprintf("[%.2fms]", float64(elapsed.Nanoseconds())/1e6)
}
