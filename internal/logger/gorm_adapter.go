package logger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// NewGormLogger 创建适配项目 slog 体系的 GORM logger。
//
// 返回的 gormlogger.Interface 将所有 GORM 日志通过项目 slog 输出，
// 从而自动进入控制台和数据库双通道，且格式与项目统一。
// slowThreshold 为慢查询阈值，超过该时间的 SQL 将以 Warn 级别输出，
// <=0 表示不启用慢查询提醒。
func NewGormLogger(slowThreshold time.Duration) gormlogger.Interface {
	l := Logger()
	if l == nil {
		return gormlogger.Default
	}
	return &gormLogAdapter{
		logger:        l,
		slowThreshold: slowThreshold,
	}
}

type gormLogAdapter struct {
	logger        *slog.Logger
	slowThreshold time.Duration
}

// LogMode 根据 GORM 日志级别返回新的 adapter。
// 注意：slog 级别由 Init 时全局控制，这里仅返回自身以满足接口。
func (a *gormLogAdapter) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return a
}

// Info 输出 GORM info 级别日志（如 AutoMigrate 等 DDL，降级为 Debug 避免终端噪音）。
func (a *gormLogAdapter) Info(ctx context.Context, msg string, args ...interface{}) {
	a.logger.DebugContext(ctx, fmt.Sprintf(msg, args...))
}

// Warn 输出 GORM warn 级别日志。
func (a *gormLogAdapter) Warn(ctx context.Context, msg string, args ...interface{}) {
	a.logger.WarnContext(ctx, fmt.Sprintf(msg, args...))
}

// Error 输出 GORM error 级别日志（降为 Debug，真正的错误由 Trace 处理）。
func (a *gormLogAdapter) Error(ctx context.Context, msg string, args ...interface{}) {
	a.logger.DebugContext(ctx, fmt.Sprintf(msg, args...))
}

// Trace 输出 GORM SQL 追踪日志。
// 真实错误以 Error 输出，ErrRecordNotFound（正常查询结果）降为 Debug，
// 慢查询以 Warn 输出，正常查询不输出。
func (a *gormLogAdapter) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		a.logger.ErrorContext(ctx, "gorm",
			"sql", sql,
			"rows", rows,
			"elapsed_ms", elapsed.Milliseconds(),
			"error", err,
		)
	case err != nil:
		a.logger.DebugContext(ctx, "gorm not found",
			"sql", sql,
			"rows", rows,
			"elapsed_ms", elapsed.Milliseconds(),
		)
	case a.slowThreshold > 0 && elapsed > a.slowThreshold:
		a.logger.WarnContext(ctx, "gorm slow query",
			"sql", sql,
			"rows", rows,
			"elapsed_ms", elapsed.Milliseconds(),
		)
	default:
		// 正常 SQL 不输出，避免终端噪音。
	}
}
