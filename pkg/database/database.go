package database

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/pkg/config"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Connect() (*gorm.DB, error) {
	return connect(false)
}

// ConnectForMigrations enables trusted multi-statement SQL from embedded files.
func ConnectForMigrations() (*gorm.DB, error) {
	return connect(true)
}

func connect(multiStatements bool) (*gorm.DB, error) {
	cfg := config.C.Database
	dsn := buildDSN(cfg, multiStatements)

	logLevel := logger.Warn
	switch strings.ToLower(cfg.LogLevel) {
	case "info":
		logLevel = logger.Info
	case "error":
		logLevel = logger.Error
	case "silent":
		logLevel = logger.Silent
	}

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, err
	}

	// 连接池配置
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 50
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	maxLifetime := cfg.ConnMaxLifetime
	if maxLifetime <= 0 {
		maxLifetime = 3600
	}
	maxIdleTime := cfg.ConnMaxIdleTime
	if maxIdleTime <= 0 {
		maxIdleTime = 300
	}

	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(maxLifetime) * time.Second)
	sqlDB.SetConnMaxIdleTime(time.Duration(maxIdleTime) * time.Second)

	// 设置默认使用 InnoDB 引擎
	db = db.Set("gorm:table_options", "ENGINE=InnoDB")

	return db, nil
}

func buildDSN(cfg config.DatabaseConfig, multiStatements bool) string {
	driverConfig := mysqldriver.NewConfig()
	driverConfig.User = cfg.User
	driverConfig.Passwd = cfg.Password
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	driverConfig.DBName = cfg.DBName
	driverConfig.ParseTime = true
	driverConfig.Loc = time.Local
	driverConfig.MultiStatements = multiStatements
	driverConfig.Params = map[string]string{"charset": "utf8mb4"}
	return driverConfig.FormatDSN()
}
