package config

import (
	"strings"
	"testing"
)

// TestGenGormConfigPostgreSQL 校验 PostgreSQL 配置生成和自定义参数剥离
func TestGenGormConfigPostgreSQL(t *testing.T) {
	conf := &ClientConf{
		Name: "gorm",
		Addr: "postgresql://user:pass@127.0.0.1:5432/app?sslmode=disable&debug=false&max_idle=7&max_active=9&max_lifetime=60",
		Slave: []string{
			"postgres://user:pass@127.0.0.2:5432/app?sslmode=disable&debug=true&max_idle=3",
		},
	}

	gormConfig := GenGormConfig(conf)

	if gormConfig.Driver != DriverPostgreSql {
		t.Fatalf("expected driver %s, got %s", DriverPostgreSql, gormConfig.Driver)
	}
	if gormConfig.DbMode {
		t.Fatal("expected debug mode disabled")
	}
	if gormConfig.MaxIdle != 7 || gormConfig.MaxActive != 9 || gormConfig.MaxLeftTime != 60 {
		t.Fatalf("unexpected pool config: %+v", gormConfig)
	}
	if strings.Contains(gormConfig.Master, "debug=") || strings.Contains(gormConfig.Master, "max_idle=") {
		t.Fatalf("master dsn contains custom params: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "sslmode=disable") {
		t.Fatalf("master dsn lost postgres params: %s", gormConfig.Master)
	}
	if len(gormConfig.Slave) != 1 {
		t.Fatalf("expected one slave dsn, got %d", len(gormConfig.Slave))
	}
	if strings.Contains(gormConfig.Slave[0], "debug=") || strings.Contains(gormConfig.Slave[0], "max_idle=") {
		t.Fatalf("slave dsn contains custom params: %s", gormConfig.Slave[0])
	}
}

// TestGenGormConfigMySQL 校验 MySQL 主从配置生成和自定义参数剥离
func TestGenGormConfigMySQL(t *testing.T) {
	conf := &ClientConf{
		Name: "gorm",
		Addr: "user:pass@tcp(127.0.0.1:3306)/app?charset=utf8mb4&parseTime=true&loc=Local&debug=true&max_idle=8&max_active=12&max_lifetime=90",
		Slave: []string{
			"user:pass@tcp(127.0.0.2:3306)/app?charset=utf8mb4&parseTime=true&loc=Local&debug=true&max_idle=3",
		},
	}

	gormConfig := GenGormConfig(conf)

	if gormConfig.Driver != DriverMysql {
		t.Fatalf("expected driver %s, got %s", DriverMysql, gormConfig.Driver)
	}
	if !gormConfig.DbMode {
		t.Fatal("expected debug mode enabled")
	}
	if gormConfig.MaxIdle != 8 || gormConfig.MaxActive != 12 || gormConfig.MaxLeftTime != 90 {
		t.Fatalf("unexpected pool config: %+v", gormConfig)
	}
	if strings.Contains(gormConfig.Master, "debug=") || strings.Contains(gormConfig.Master, "max_idle=") {
		t.Fatalf("master dsn contains custom params: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "parseTime=true") {
		t.Fatalf("master dsn lost mysql params: %s", gormConfig.Master)
	}
	if len(gormConfig.Slave) != 1 {
		t.Fatalf("expected one slave dsn, got %d", len(gormConfig.Slave))
	}
	if strings.Contains(gormConfig.Slave[0], "debug=") || strings.Contains(gormConfig.Slave[0], "max_idle=") {
		t.Fatalf("slave dsn contains custom params: %s", gormConfig.Slave[0])
	}
}

// TestGenGormConfigPostgreSQLKeyValue 校验 PostgreSQL key=value 格式 DSN 配置生成
func TestGenGormConfigPostgreSQLKeyValue(t *testing.T) {
	conf := &ClientConf{
		Name: "posgresql",
		Addr: "host=127.0.0.1 user=postgres password=123456 dbname=sub2api port=5432 sslmode=disable TimeZone=Asia/Shanghai",
	}

	gormConfig := GenGormConfig(conf)

	if gormConfig.Driver != DriverPostgreSql {
		t.Fatalf("expected driver %s, got %s", DriverPostgreSql, gormConfig.Driver)
	}
	if strings.Contains(gormConfig.Master, "debug=") || strings.Contains(gormConfig.Master, "max_idle=") {
		t.Fatalf("master dsn contains unexpected custom params: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "host=") {
		t.Fatalf("master dsn lost host param: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "dbname=") {
		t.Fatalf("master dsn lost dbname param: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "sslmode=disable") {
		t.Fatalf("master dsn lost sslmode param: %s", gormConfig.Master)
	}
	t.Logf("PostgreSQL key-value DSN: %s", gormConfig.Master)
}

// TestGenGormConfigPostgreSQLKeyValueWithCustomParams 校验带自定义参数的 key-value DSN
func TestGenGormConfigPostgreSQLKeyValueWithCustomParams(t *testing.T) {
	conf := &ClientConf{
		Name: "posgresql",
		Addr: "host=127.0.0.1 user=postgres password=123456 dbname=test port=5432 sslmode=disable debug=true max_idle=10 max_active=20 max_lifetime=120",
	}

	gormConfig := GenGormConfig(conf)

	if gormConfig.Driver != DriverPostgreSql {
		t.Fatalf("expected driver %s, got %s", DriverPostgreSql, gormConfig.Driver)
	}
	if !gormConfig.DbMode {
		t.Fatal("expected debug mode enabled")
	}
	if gormConfig.MaxIdle != 10 || gormConfig.MaxActive != 20 || gormConfig.MaxLeftTime != 120 {
		t.Fatalf("unexpected pool config: %+v", gormConfig)
	}
	if strings.Contains(gormConfig.Master, "debug=") {
		t.Fatalf("master dsn contains debug param: %s", gormConfig.Master)
	}
	if strings.Contains(gormConfig.Master, "max_idle=") {
		t.Fatalf("master dsn contains max_idle param: %s", gormConfig.Master)
	}
	if strings.Contains(gormConfig.Master, "max_active=") {
		t.Fatalf("master dsn contains max_active param: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "host=") {
		t.Fatalf("master dsn lost host param: %s", gormConfig.Master)
	}
	if !strings.Contains(gormConfig.Master, "dbname=") {
		t.Fatalf("master dsn lost dbname param: %s", gormConfig.Master)
	}
	t.Logf("cleaned PostgreSQL key-value DSN: %s", gormConfig.Master)
}
