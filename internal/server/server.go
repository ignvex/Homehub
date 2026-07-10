// Package server 提供 HomeHub HTTP 服务的启动、路由注册与优雅关闭。
//
// 基于 Gin 框架，挂载全局中间件（recovery / logger / auth），
// 注册 API 路由与页面路由，提供信号驱动的优雅关闭。
package server

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/middleware"
)

// Run 启动 HTTP 服务并阻塞，直至收到 SIGINT/SIGTERM 后优雅关闭。
//
// 启动序列：
//  1. 根据配置设置 Gin 运行模式
//  2. 创建 Gin 引擎
//  3. 挂载全局中间件
//  4. 加载模板与静态文件
//  5. 注册所有路由
//  6. 启动 HTTP server
//  7. 等待信号，触发优雅关闭（5s 超时）
func Run() error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("server: 配置未加载，请先调用 config.Load")
	}

	// 根据日志级别设置 Gin 模式
	if cfg.Log.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// 全局中间件
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())

	// 静态文件与模板
	r.Static("/static", "./web/static")
	r.LoadHTMLGlob("web/templates/*.html")

	// 注册路由
	registerRoutes(r)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 信号监听
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动 HTTP server
	go func() {
		logger.Info("HTTP 服务启动", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP 服务异常退出", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("收到终止信号，开始优雅关闭...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("服务关闭失败", "error", err)
		return err
	}

	logger.Info("服务已安全关闭")
	return nil
}
