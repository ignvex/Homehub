// HomeHub 个人中枢 — 集成导航页面、书签管理、配置管理等功能。
//
// 启动序列：
//
//	config.Load → logger.Init → database.Init → AutoMigrate
//	→ seed user → handler.Init → server.Run
//
// @title           HomeHub API
// @version         1.0
// @description     个人中枢项目 API 接口文档，包含认证、书签管理、配置管理等功能。
// @contact.name    HomeHub
// @host            localhost:8080
// @BasePath        /api/v1
package main

import (
	"context"
	"log"
	"os"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/database"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/server"
	"github.com/ignvex/HomeHub/internal/service"
)

func main() {
	// ============================================================
	// 1. 加载配置
	// ============================================================
	configPath := "config.json"
	if v := os.Getenv("HOMEHUB_CONFIG"); v != "" {
		configPath = v
	}

	if err := config.Load(configPath); err != nil {
		// 首次启动生成默认配置文件
		log.Printf("[main] %v", err)
		os.Exit(0)
	}

	cfg := config.Get()

	// ============================================================
	// 2. 初始化日志（控制台输出）
	// ============================================================
	logger.Init(cfg.Log.Level, cfg.Log.Format)
	logger.Info("HomeHub 启动中...")

	// 启动配置文件热加载
	go config.Watch(configPath)

	// ============================================================
	// 3. 初始化数据库
	// ============================================================
	db, err := database.Init(&cfg.Database)
	if err != nil {
		logger.Error("数据库初始化失败", "error", err)
		os.Exit(1)
	}
	defer func() {
		logger.CloseDBWriter()
		database.Close()
	}()

	// ============================================================
	// 4. 启用数据库日志写入（若配置开启）
	// ============================================================
	if cfg.Log.DBWriter {
		logger.AddDBWriter(db, cfg.Log.Level)
		logger.Info("数据库日志写入已启用")
	}

	// ============================================================
	// 5. 自动迁移（GORM AutoMigrate）
	// ============================================================
	if err := db.AutoMigrate(
		&model.User{},
		&model.Session{},
		&model.BookmarkGroup{},
		&model.Bookmark{},
		&model.LogEntry{},
		&model.MediaAsset{},
		&model.AvatarCache{},
		&model.RSSFeed{},
		&model.RSSEntry{},
		&model.Todo{},
	); err != nil {
		logger.Error("数据库迁移失败", "error", err)
		os.Exit(1)
	}
	logger.Info("数据库迁移完成")

	// ============================================================
	// 6. 创建初始用户（若不存在）
	// ============================================================
	svc := service.NewServices(db)

	if cfg.Auth.Password != "" {
		if err := svc.Auth.CreateInitialUser(cfg.Auth.Username, cfg.Auth.Password, "admin"); err != nil {
			logger.Error("创建初始用户失败", "error", err)
			os.Exit(1)
		}
	} else {
		logger.Warn("auth.password 为空，跳过初始用户创建（请通过 Web 端或 config.json 设置密码）")
	}

	// 启动 RSS 后台定时抓取（通过 context 控制生命周期，defer 在程序退出时停止）
	rssCtx, rssCancel := context.WithCancel(context.Background())
	defer rssCancel()
	svc.RSS.StartBackgroundFetcher(rssCtx)

	// ============================================================
	// 7. 注入 Handler
	// ============================================================
	handler.Init(svc)

	// ============================================================
	// 8. 启动 HTTP 服务
	// ============================================================
	if err := server.Run(); err != nil {
		logger.Error("服务异常退出", "error", err)
		os.Exit(1)
	}
}
