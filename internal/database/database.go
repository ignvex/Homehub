// Package database 提供 HomeHub 的数据库连接管理与基础操作。
//
// 基于 GORM + pgx 连接 PostgreSQL，负责初始化连接池、执行自动迁移
// 以及提供全局 *gorm.DB 实例供其他包使用。
//
// 使用方式：
//
//	cfg := config.Get()
//	db, err := database.Init(&cfg.Database)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer database.Close()
//
//	// 其他包中直接获取：
//	db := database.DB()
package database

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// 连接池默认参数（生产环境建议值）。
const (
	defaultMaxIdleConns    = 10
	defaultMaxOpenConns    = 100
	defaultConnMaxLifetime = 1 * time.Hour
	defaultConnMaxIdleTime = 10 * time.Minute
)

var (
	gDB     *gorm.DB  // 全局数据库实例
	gDBOnce sync.Once // 确保 Init 只执行一次
)

// DB 返回全局 *gorm.DB 实例。
//
// 调用前必须已通过 Init 完成初始化，否则返回 nil。
func DB() *gorm.DB {
	return gDB
}

// Init 根据数据库配置初始化 GORM 连接并配置连接池。
//
// cfg 由 config.Get().Database 传入。
// 该函数通过 sync.Once 保证全局仅初始化一次，重复调用安全无副作用。
// 返回的 *gorm.DB 同时存入 gDB，后续可通过 DB() 获取。
func Init(cfg *config.DatabaseConfig) (*gorm.DB, error) {
	var initErr error
	gDBOnce.Do(func() {
		if cfg == nil {
			initErr = errors.New("database: 配置不能为 nil")
			return
		}

		dsn := buildDSN(cfg)

		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Warn),
		})
		if err != nil {
			initErr = fmt.Errorf("database: 连接 PostgreSQL 失败: %w", err)
			return
		}

		// 配置底层 *sql.DB 连接池
		sqlDB, err := db.DB()
		if err != nil {
			initErr = fmt.Errorf("database: 获取 sql.DB 失败: %w", err)
			return
		}

		sqlDB.SetMaxIdleConns(defaultMaxIdleConns)
		sqlDB.SetMaxOpenConns(defaultMaxOpenConns)
		sqlDB.SetConnMaxLifetime(defaultConnMaxLifetime)
		sqlDB.SetConnMaxIdleTime(defaultConnMaxIdleTime)

		// 验证连接可达
		if err := sqlDB.Ping(); err != nil {
			initErr = fmt.Errorf("database: 数据库 Ping 失败: %w", err)
			return
		}

		gDB = db
	})

	if initErr != nil {
		return nil, initErr
	}
	return gDB, nil
}

// Close 关闭底层数据库连接池。
//
// 应在程序退出前调用（如 defer database.Close()）。
// 若未初始化则无操作。
func Close() {
	if gDB != nil {
		if sqlDB, err := gDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// buildDSN 根据 DatabaseConfig 构建 PostgreSQL DSN 字符串。
//
// DSN 格式为 key=value 对，空格分隔，与 pgx/lib/pq 兼容。
func buildDSN(cfg *config.DatabaseConfig) string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode,
	)
}

// Ping 检测数据库连接是否正常。
//
// 返回 nil 表示连接可达，否则返回错误。
func Ping() error {
	if gDB == nil {
		return errors.New("database: 未初始化，请先调用 Init")
	}
	sqlDB, err := gDB.DB()
	if err != nil {
		return fmt.Errorf("database: 获取 sql.DB 失败: %w", err)
	}
	return sqlDB.Ping()
}

// Stats 返回底层连接池的统计信息。
//
// 可用于监控连接泄漏和资源使用情况。
// 若未初始化则返回 nil。
func Stats() *sql.DBStats {
	if gDB == nil {
		return nil
	}
	sqlDB, err := gDB.DB()
	if err != nil {
		return nil
	}
	s := sqlDB.Stats()
	return &s
}
