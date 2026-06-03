//go:build integration

package server

import (
	"context"
	"testing"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/orm"
	apctracing "github.com/ethereal3x/apc/tracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegrationTracingLog 验证 tracing 初始化和带 trace 上下文日志输出
func TestIntegrationTracingLog(t *testing.T) {
	cfg := config.GetConf()

	shutdown, err := apctracing.InitProvider(cfg.Plugin.Tracing)
	require.NoError(t, err, "初始化 tracing provider 失败")
	defer func() { assert.NoError(t, shutdown(context.Background())) }()

	ctx, span := apctracing.Start(context.Background(), "integration-test-root")
	defer span.End()
	traceID := apctracing.TraceID(ctx)
	require.NotEmpty(t, traceID, "TraceID 不应为空")
	t.Logf("TraceID: %s", traceID)

	childCtx, childSpan := apctracing.Start(ctx, "integration-test-child")
	defer childSpan.End()
	assert.Equal(t, traceID, apctracing.TraceID(childCtx), "父子 span 的 TraceID 必须一致")

	log := logger.NewLogger(&cfg.Plugin.Log)
	log.ContextInfo(ctx, "tracing 链路日志测试")
	log.ContextInfo(childCtx, "子 span 日志测试")
	log.Info("普通 info 日志测试")
	log.Debug("debug 日志测试")

	logger.SetLogger(log)
	assert.NotPanics(t, func() { logger.ContextInfo(ctx, "全局 logger 日志测试") })
	t.Log("tracing + log 连通性验证通过")
}

// TestIntegrationGormConnect 验证 GORM 数据库连接
func TestIntegrationGormConnect(t *testing.T) {
	cfg := config.GetConf()

	shutdown, _ := apctracing.InitProvider(cfg.Plugin.Tracing)
	if shutdown != nil {
		defer func() { _ = shutdown(context.Background()) }()
	}
	log := logger.NewLogger(&cfg.Plugin.Log)
	logger.SetLogger(log)

	testCases := []struct {
		clientName string
		versionSQL string
	}{
		{clientName: "mysql", versionSQL: "SELECT VERSION()"},
		{clientName: "posgresql", versionSQL: "SELECT version()"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.clientName, func(t *testing.T) {
			clientConf := config.GetClientConf(testCase.clientName)
			if clientConf == nil {
				t.Skipf("未找到 %s 配置", testCase.clientName)
				return
			}
			gormConfig := config.GenGormConfig(clientConf)
			t.Logf("Driver: %s, DSN: %s", gormConfig.Driver, gormConfig.Master)

			db, err := orm.NewGormInstance(gormConfig)
			if err != nil {
				t.Skipf("连接失败: %v", err)
				return
			}
			sqlDB, err := db.DB()
			require.NoError(t, err, "获取 sql.DB 失败")
			defer sqlDB.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, sqlDB.PingContext(ctx), "数据库 ping 失败")

			var result int
			db.WithContext(ctx).Raw("SELECT 1").Scan(&result)
			assert.Equal(t, 1, result)

			traceCtx, span := apctracing.Start(ctx, "gorm-version-query")
			var version string
			db.WithContext(traceCtx).Raw(testCase.versionSQL).Scan(&version)
			span.End()
			t.Logf("Version: %s", version)
			assert.NotEmpty(t, version)
		})
	}
}
