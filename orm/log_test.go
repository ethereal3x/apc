package orm

import (
	"context"
	"errors"
	"testing"
	"time"

	apcLogger "github.com/ethereal3x/apc/logger"
	"gorm.io/gorm/logger"
)

// TestGormLoggerTraceLevel 校验 Trace 按日志级别决定是否构造 SQL 日志
func TestGormLoggerTraceLevel(t *testing.T) {
	apcLogger.SetLogger(apcLogger.NewLogger())
	testCases := []struct {
		name       string
		level      logger.LogLevel
		err        error
		wantCalled bool
	}{
		{name: "silent skips trace", level: logger.Silent, wantCalled: false},
		{name: "error skips success trace", level: logger.Error, wantCalled: false},
		{name: "error logs error trace", level: logger.Error, err: errors.New("query failed"), wantCalled: true},
		{name: "info logs success trace", level: logger.Info, wantCalled: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			gormLogger := NewGormDBLog().LogMode(testCase.level)

			gormLogger.Trace(context.Background(), time.Now(), func() (string, int64) {
				called = true
				return "select 1", 1
			}, testCase.err)

			if called != testCase.wantCalled {
				t.Fatalf("expected fc called %v, got %v", testCase.wantCalled, called)
			}
		})
	}
}
