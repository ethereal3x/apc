package logger

import (
	"fmt"
	"go.uber.org/zap"
	"testing"
)

func TestLog(t *testing.T) {
	LogInit(Config{
		Level:      LevelDebug,
		Format:     FormatJSON,
		OutputPath: "app.log",
	})

	logger.Info("服务启动成功", zap.String("version", "1.0.0"))
	logger.Debug("调试日志", zap.Int("uid", 123))
	logger.Error("出错了", zap.Error(fmt.Errorf("数据库连接失败")))

	defer Sync()
}
