// Package logger 提供 HomeHub 全局结构化日志。
//
// 基于 log/slog，支持双通道输出：
//   - 控制台 handler：彩色文本输出（通过 colored 格式启用）
//   - 数据库 handler：异步批量写入 PostgreSQL（通过 AddDBWriter 启用）
//
// 架构上使用 slog 多 handler 模式，两个 handler 独立工作互不干扰。
// 便捷函数 Debug/Info/Warn/Error 对全局 Logger 加 nil 保护。
package logger

import (
	"log/slog"
	"os"

	"gorm.io/gorm"
)

var (
	lg      *slog.Logger
	dbh     *dbHandler // 保留引用，用于优雅关闭
)

// Init 初始化全局 slog.Logger，使用控制台 handler。
//
// level 取值 debug / info / warn / error，无效值默认 info。
// format 取值 json / colored：
//   - "json"：slog.NewJSONHandler 输出到 os.Stdout（适合 Docker/生产）
//   - "colored"：自定义 consoleHandler，彩色文本输出（适合开发终端）
//
// 同时调用 slog.SetDefault 使标准库 slog 默认方法也指向同一实例。
func Init(level string, format string) {
	l := parseLevel(level)

	opts := &slog.HandlerOptions{
		Level:     l,
		AddSource: true,
	}

	var handler slog.Handler
	switch format {
	case "colored":
		handler = newConsoleHandler(os.Stdout, opts, true)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	lg = slog.New(handler)
	slog.SetDefault(lg)
}

// AddDBWriter 注入数据库写入 handler，与已有控制台 handler 合并为多 handler。
//
// 调用前必须已通过 Init 初始化控制台 handler，且 DB 已就绪。
// 若 db 为 nil 或 lg 未初始化，则无操作。
func AddDBWriter(db *gorm.DB, level string) {
	if db == nil || lg == nil {
		return
	}

	dbh = newDBHandler(db, parseLevel(level))

	// 构建多 handler：控制台 + 数据库
	lg = slog.New(&multiHandler{
		handlers: []slog.Handler{lg.Handler(), dbh},
	})
	slog.SetDefault(lg)
}

// CloseDBWriter 关闭数据库 handler，等待未写入的日志刷盘。
//
// 应在程序退出前、database.Close() 之前调用。
func CloseDBWriter() {
	if dbh != nil {
		dbh.Close()
	}
}

// parseLevel 将字符串日志级别转为 slog.Level，无效值默认 Info。
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Debug 输出 debug 级别日志。
func Debug(msg string, args ...any) {
	if lg != nil {
		lg.Debug(msg, args...)
	}
}

// Info 输出 info 级别日志。
func Info(msg string, args ...any) {
	if lg != nil {
		lg.Info(msg, args...)
	}
}

// Warn 输出 warn 级别日志。
func Warn(msg string, args ...any) {
	if lg != nil {
		lg.Warn(msg, args...)
	}
}

// Error 输出 error 级别日志。
func Error(msg string, args ...any) {
	if lg != nil {
		lg.Error(msg, args...)
	}
}

// Logger 返回全局 *slog.Logger 实例。
//
// 调用方可通过返回值进行更复杂的日志操作（如 With/WithGroup）。
func Logger() *slog.Logger {
	return lg
}
