// Package app 承载 HomeHub 的启动编排。
//
// 从 cmd/homehub 中分离出来，使启动顺序可以被测试，
// 也让将来的其他入口（如只跑抓取的 worker）复用同一套初始化。
package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/database"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/server"
	"github.com/ignvex/HomeHub/internal/service"
)

// Options 启动参数。
type Options struct {
	// ConfigPath 显式指定的配置文件路径，优先级最高。
	// 为空时回落到 HOMEHUB_CONFIG，再回落到 config.json。
	ConfigPath string
}

// resolveConfigPath 按优先级确定配置文件路径。
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv("HOMEHUB_CONFIG"); v != "" {
		return v
	}
	return "config.json"
}

// Run 启动 HomeHub，阻塞直到 HTTP 服务退出。
//
// 返回的 error 已包含足够的上下文，调用方只需决定退出码，
// 无需再次打印（日志已在故障点记录）。
func Run(opts Options) error {
	configPath := resolveConfigPath(opts.ConfigPath)

	// ============================================================
	// 1. 加载配置
	// ============================================================
	if err := config.Load(configPath); err != nil {
		if errors.Is(err, config.ErrConfigGenerated) {
			return fmt.Errorf("已生成默认配置 %s，请填写数据库连接等信息后重新启动", configPath)
		}
		return err
	}

	cfg := config.Get()

	// ============================================================
	// 2. 初始化日志
	// ============================================================
	logger.Init(cfg.Log.Level, cfg.Log.Format)
	logger.Info("HomeHub 启动中...", "config", configPath)

	// 配置热加载
	go config.Watch(configPath)

	// ============================================================
	// 3. 数据库
	// ============================================================
	db, err := database.Init(&cfg.Database)
	if err != nil {
		return fmt.Errorf("数据库初始化失败: %w", err)
	}
	defer func() {
		logger.CloseDBWriter()
		database.Close()
	}()

	if cfg.Log.DBWriter {
		logger.AddDBWriter(db, cfg.Log.Level)
		logger.Info("数据库日志写入已启用")
	}

	// ============================================================
	// 4. 表结构（AutoMigrate + 少量手工 DDL）
	// ============================================================
	if err := db.AutoMigrate(
		&model.User{},
		&model.Session{},
		&model.BookmarkGroup{},
		&model.Bookmark{},
		&model.LogEntry{},
		&model.MediaAsset{},
		&model.AvatarCache{},
		&model.RSSGroup{},
		&model.RSSFeed{},
		&model.RSSEntry{},
		&model.RSSImage{},
		&model.RSSImageRef{},
		&model.Todo{},
	); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}

	// pastes 表带建表选项，不交给 AutoMigrate。
	// 业务层用 Unscoped 物理删除，deleted_at 列保留是为与 BaseModel 对齐。
	if err := model.CreatePastesTable(db); err != nil {
		return fmt.Errorf("创建 pastes 表失败: %w", err)
	}

	// 模糊搜索索引（pg_trgm）。可选优化，失败仅告警，不阻塞启动。
	database.EnsureSearchIndexes(db)

	logger.Info("数据库结构就绪")

	// ============================================================
	// 5. 初始用户
	// ============================================================
	svc := service.NewServices(db)

	if cfg.Auth.Password != "" {
		if err := svc.Auth.CreateInitialUser(cfg.Auth.Username, cfg.Auth.Password, "admin"); err != nil {
			return fmt.Errorf("创建初始用户失败: %w", err)
		}
		config.ClearPassword(configPath)
	} else {
		logger.Warn("auth.password 为空，跳过初始用户创建（请通过 Web 端或 config.json 设置密码）")
	}

	// ============================================================
	// 6. 后台任务：统一一个可取消的根 context
	// ============================================================
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go service.CleanupExpiredSessions(ctx, db)
	svc.RSS.StartBackgroundFetcher(ctx)
	svc.RSS.StartImageJanitor(ctx)
	svc.Balance.StartBackgroundRefresh(ctx)
	svc.Paste.StartPurgeLoop(ctx)

	logger.Info("后台任务已启动",
		"rss", cfg.RSS.BackgroundFetchEnabled,
		"balance", cfg.BalanceMonitor.Enabled,
		"pastebin", cfg.Pastebin.Enabled,
	)

	// ============================================================
	// 7. HTTP 服务
	// ============================================================
	handler.Init(svc)
	return server.Run()
}
