package orm

import (
	"fmt"
	"time"

	"github.com/ethereal3x/apc/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

// NewGormInstance 创建 Gorm 数据库实例
func NewGormInstance(dbConf *config.GormConfig, conf ...*gorm.Config) *gorm.DB {
	gormConfig := &gorm.Config{}
	if len(conf) > 0 && conf[0] != nil {
		configValue := *conf[0]
		gormConfig = &configValue
	}
	if gormConfig.Logger == nil {
		logLevel := logger.Silent
		if dbConf.DbMode {
			logLevel = logger.Info
		}
		gormConfig.Logger = NewGormDBLog().LogMode(logLevel)
	}

	// 初始化数据库连接
	db, err := gorm.Open(newDBDriver(dbConf), gormConfig)
	if err != nil {
		panic(fmt.Errorf("open gorm db: %w", err))
	}
	if dbConf.DbMode {
		db = db.Debug()
	}
	if len(dbConf.Slave) > 0 {
		// 注册读写分离连接池
		if err := connectRWDB(dbConf, db); err != nil {
			panic(fmt.Errorf("connect read write db: %w", err))
		}
		return db
	}
	// 初始化单节点连接池
	if err := connectDB(dbConf, db); err != nil {
		panic(fmt.Errorf("connect db: %w", err))
	}
	return db
}

// connectDB 初始化单节点数据库连接池
func connectDB(dbConf *config.GormConfig, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql db: %w", err)
	}
	sqlDB.SetMaxIdleConns(dbConf.MaxIdle)
	sqlDB.SetMaxOpenConns(dbConf.MaxActive)
	sqlDB.SetConnMaxLifetime(time.Duration(dbConf.MaxLeftTime) * time.Second)
	return nil
}

// connectRWDB 注册读写分离数据库连接池
func connectRWDB(dbConf *config.GormConfig, db *gorm.DB) error {
	masterDialector := createDialector(dbConf.Driver, dbConf.Master)
	replicas := make([]gorm.Dialector, 0, len(dbConf.Slave))
	for _, slaveDSN := range dbConf.Slave {
		slaveDialector := createDialector(dbConf.Driver, slaveDSN)
		replicas = append(replicas, slaveDialector)
	}
	if err := db.Use(
		dbresolver.Register(dbresolver.Config{
			Sources:  []gorm.Dialector{masterDialector},
			Replicas: replicas,
			Policy:   dbresolver.RandomPolicy{},
		}).
			SetMaxIdleConns(dbConf.MaxIdle).
			SetConnMaxLifetime(time.Duration(dbConf.MaxLeftTime) * time.Second).
			SetMaxOpenConns(dbConf.MaxActive),
	); err != nil {
		return fmt.Errorf("register db resolver: %w", err)
	}
	return nil
}

// driverFactory 数据库驱动工厂函数映射表
var driverFactory = map[string]func(string) gorm.Dialector{
	config.DriverMysql:      mysql.Open,
	config.DriverPostgreSql: postgres.Open,
}

// dialectorFactory 创建带配置的 Dialector 工厂函数映射表
var dialectorFactory = map[string]func(string) gorm.Dialector{
	config.DriverMysql: func(dsn string) gorm.Dialector {
		cfg := mysql.Config{DSN: dsn}
		return mysql.New(cfg)
	},
	config.DriverPostgreSql: func(dsn string) gorm.Dialector {
		cfg := postgres.Config{DSN: dsn}
		return postgres.New(cfg)
	},
}

// newDBDriver 根据配置创建数据库驱动
func newDBDriver(dbConf *config.GormConfig) gorm.Dialector {
	if factory, exists := driverFactory[dbConf.Driver]; exists {
		return factory(dbConf.Master)
	}
	panic(fmt.Sprintf("driver %s not supported", dbConf.Driver))
}

// createDialector 根据驱动类型和 DSN 创建 Dialector
func createDialector(driver string, dsn string) gorm.Dialector {
	if factory, exists := dialectorFactory[driver]; exists {
		return factory(dsn)
	}
	if factory, exists := driverFactory[driver]; exists {
		return factory(dsn)
	}
	panic(fmt.Sprintf("driver %s not supported", driver))
}
