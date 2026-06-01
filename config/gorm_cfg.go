package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/spf13/cast"
)

const (
	DriverMysql      = "mysql"
	DriverPostgreSql = "postgresql"
)

type GormConfig struct {
	Master      string   // 主节点连接地址
	Slave       []string // 从节点连接地址
	DbMode      bool     // 是否开启日志打印
	MaxIdle     int      // 最大连接数
	MaxActive   int      // 最大活跃连接数
	MaxLeftTime int      // 连接存活时间
	Driver      string   // 驱动类型
}

var customParams = map[string]bool{
	"max_idle":     true,
	"max_active":   true,
	"max_lifetime": true,
	"debug":        true,
	"driver":       true,
}

// GenGormConfig 生成 Gorm 数据库配置
func GenGormConfig(conf *ClientConf) *GormConfig {
	if conf == nil {
		panic("conf is nil")
	}
	driver := detectDriver(conf.Addr)
	params, masterDSN, err := parseDatabaseDSN(driver, conf.Addr)
	if err != nil {
		panic(fmt.Errorf("parse master dsn: %w", err))
	}
	slaveDSNList := make([]string, 0, len(conf.Slave))
	for _, slaveDSN := range conf.Slave {
		_, parsedSlaveDSN, err := parseDatabaseDSN(driver, slaveDSN)
		if err != nil {
			panic(fmt.Errorf("parse slave dsn: %w", err))
		}
		slaveDSNList = append(slaveDSNList, parsedSlaveDSN)
	}
	return &GormConfig{
		Slave:       slaveDSNList,
		DbMode:      cast.ToBool(getGormParam(params, "debug", true)),
		MaxIdle:     cast.ToInt(getGormParam(params, "max_idle", 25)),
		MaxActive:   cast.ToInt(getGormParam(params, "max_active", 25)),
		MaxLeftTime: cast.ToInt(getGormParam(params, "max_lifetime", 300)),
		Driver:      driver,
		Master:      masterDSN,
	}
}

// parseDatabaseDSN 按数据库驱动解析并清理连接地址
func parseDatabaseDSN(driver string, addr string) (map[string]string, string, error) {
	switch driver {
	case DriverMysql:
		return parseMySQLDSN(addr)
	case DriverPostgreSql:
		return parsePostgreSQLDSN(addr)
	default:
		return nil, "", fmt.Errorf("driver %s not supported", driver)
	}
}

// parseMySQLDSN 解析 MySQL DSN 并剥离 Gorm 自定义参数
func parseMySQLDSN(addr string) (map[string]string, string, error) {
	urlConfig, err := mysql.ParseDSN(addr)
	if err != nil {
		return nil, "", fmt.Errorf("parse mysql dsn: %w", err)
	}
	params := urlConfig.Params
	urlConfig.Params = make(map[string]string)
	for key, value := range params {
		if _, ok := customParams[key]; ok {
			continue
		}
		urlConfig.Params[key] = value
	}
	return params, urlConfig.FormatDSN(), nil
}

// parsePostgreSQLDSN 解析 PostgreSQL DSN 并剥离 Gorm 自定义参数
func parsePostgreSQLDSN(addr string) (map[string]string, string, error) {
	urlConfig, err := url.Parse(addr)
	if err != nil {
		return nil, "", fmt.Errorf("parse postgresql dsn: %w", err)
	}
	if urlConfig.Scheme != DriverPostgreSql && urlConfig.Scheme != "postgres" {
		return nil, "", fmt.Errorf("invalid postgresql dsn scheme: %s", urlConfig.Scheme)
	}
	params := make(map[string]string)
	queryValues := urlConfig.Query()
	for key, values := range queryValues {
		if len(values) == 0 {
			continue
		}
		params[key] = values[0]
		if _, ok := customParams[key]; ok {
			queryValues.Del(key)
		}
	}
	urlConfig.RawQuery = queryValues.Encode()
	return params, urlConfig.String(), nil
}

// detectDriver 识别数据库连接地址对应的驱动类型
func detectDriver(dsn string) string {
	lowerDSN := strings.ToLower(dsn)
	if strings.HasPrefix(lowerDSN, "postgresql://") || strings.HasPrefix(lowerDSN, "postgres://") {
		return DriverPostgreSql
	}
	return DriverMysql
}

// getGormParam 获取 Gorm 自定义参数
func getGormParam(params map[string]string, key string, defaultValue any) any {
	if value, ok := params[key]; ok {
		return value
	}
	return defaultValue
}
