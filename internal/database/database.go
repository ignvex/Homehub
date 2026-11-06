// Package database 提供 HomeHub 的数据库连接管理与基础操作。
//
// 基于 GORM + pgx 连接 PostgreSQL，负责初始化连接池、执行自动迁移
// 以及提供全局 *gorm.DB 实例供其他包使用。
//
// 使用方式：
//
//	cfg := config.Get()
//	db, err := database.Init(ctx, &cfg.Database, time.Minute)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer database.Close()
//
//	// 其他包中直接获取：
//	db := database.DB()
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
//
// 数据库暂时不可达时（网络不通、DNS 未就绪、PostgreSQL 仍在启动），
// 在 maxWait 内带退避重试；maxWait <= 0 表示只尝试一次。认证失败、
// 库不存在这类重试也不会好转的错误立即返回。ctx 取消时立即放弃，
// 返回的错误包装 ctx.Err()。
func Init(ctx context.Context, cfg *config.DatabaseConfig, maxWait time.Duration) (*gorm.DB, error) {
	var initErr error
	gDBOnce.Do(func() {
		if cfg == nil {
			initErr = errors.New("database: 配置不能为 nil")
			return
		}

		db, err := openWithRetry(ctx, cfg, maxWait)
		if err != nil {
			initErr = err
			return
		}

		gDB = db
	})

	if initErr != nil {
		return nil, initErr
	}
	return gDB, nil
}

// 启动重试的退避参数。
const (
	connectRetryInitial = 1 * time.Second
	connectRetryMax     = 5 * time.Second
)

// connectAttemptTimeout 是单次连接尝试的上限。
//
// pgx 默认的拨号器没有超时：数据库主机丢包而不是拒绝连接（NAS 关机、
// 路由不通）时，一次尝试要等内核 SYN 重传耗尽，约两分钟。这期间既看不到
// 信号，也越过了 maxWait。每次尝试都套一个短超时，并且跟随 ctx 取消。
const connectAttemptTimeout = 5 * time.Second

// openWithRetry 在 maxWait 内反复尝试 open，直到成功、遇到不可重试的错误或 ctx 取消。
func openWithRetry(ctx context.Context, cfg *config.DatabaseConfig, maxWait time.Duration) (*gorm.DB, error) {
	deadline := time.Now().Add(maxWait)
	delay := connectRetryInitial
	for attempt := 1; ; attempt++ {
		attemptTimeout := connectAttemptTimeout
		if left := time.Until(deadline); maxWait > 0 && left > 0 && left < attemptTimeout {
			attemptTimeout = left
		}
		db, err := open(ctx, cfg, attemptTimeout)
		if err == nil {
			if attempt > 1 {
				logger.Info("数据库已就绪", "attempts", attempt)
			}
			return db, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("database: 等待数据库时被取消: %w", ctx.Err())
		}
		if !retryableConnectError(err) || !time.Now().Add(delay).Before(deadline) {
			return nil, err
		}
		logger.Warn("数据库暂不可达，稍后重试",
			"host", cfg.Host, "attempt", attempt, "retry_in", delay.String(), "error", err)

		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("database: 等待数据库时被取消: %w", ctx.Err())
		case <-t.C:
		}
		delay = min(delay*2, connectRetryMax)
	}
}

// open 建立一次连接并配置连接池，失败时不留下打开的连接池。
//
// 可达性检查用带超时的 PingContext，而不是 gorm.Open 自带的 Ping：
// 后者用 context.Background()，拨号卡住时无法打断。
func open(ctx context.Context, cfg *config.DatabaseConfig, timeout time.Duration) (*gorm.DB, error) {
	dsn := buildDSN(cfg)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:               logger.NewGormLogger(200 * time.Millisecond),
		DisableAutomaticPing: true,
	})
	if err != nil {
		return nil, fmt.Errorf("database: 连接 PostgreSQL 失败 (主机=%s): %w", cfg.Host, err)
	}

	// 配置底层 *sql.DB 连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database: 获取 sql.DB 失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(defaultMaxIdleConns)
	sqlDB.SetMaxOpenConns(defaultMaxOpenConns)
	sqlDB.SetConnMaxLifetime(defaultConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(defaultConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("database: 数据库 Ping 失败 (主机=%s): %w", cfg.Host, err)
	}

	return db, nil
}

// retryableConnectError 判断启动时的连接错误是否值得重试。
//
// 服务端明确拒绝的错误（认证失败、库不存在、权限不足）重试只会让
// 用户多等一分钟才看到同一条报错，立即返回；DSN 解析失败同理。唯一的例外是 57P03
// （cannot_connect_now）：PostgreSQL 正在启动或恢复，稍后就能连上。
// 其余错误——连接被拒、超时、DNS 解析失败——都是数据库或网络
// 还没就绪的典型表现，值得等待。
func retryableConnectError(err error) bool {
	// DSN 解析失败（例如密码里带空格）是配置问题，等多久都一样。
	var parseErr *pgconn.ParseConfigError
	if errors.As(err, &parseErr) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "57P03"
	}
	return true
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
