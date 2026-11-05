// Package server 提供 HomeHub HTTP 服务的启动、路由注册与优雅关闭。
//
// 基于 Gin 框架，挂载全局中间件（recovery / logger / auth），
// 注册 API 路由与页面路由，提供信号驱动的优雅关闭。
package server

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/ignvex/HomeHub/docs"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/middleware"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// 服务器超时。
//
// 只设读请求头与空闲超时，**不设 WriteTimeout**：日志 SSE 流
// （/admin/logs/stream）是长连接，WriteTimeout 会把它按时掐断。
// ReadHeaderTimeout 挡住慢速发送请求头的连接（Slowloris）占满 fd。
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
)

// Go 内置的 MIME 表没有 .webmanifest，文件服务会退回到系统的 mime.types；
// 精简容器镜像里常常没有这个文件，manifest 就会按内容嗅探成 text/plain，
// 浏览器随之拒绝把应用识别为可安装的 PWA。包初始化时补登记一次。
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// newHTTPServer 构造带超时设置的 http.Server。
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// newEngine 创建 Gin 引擎并设置可信代理。
//
// Gin 默认信任所有来源的 X-Forwarded-For，c.ClientIP() 会采用客户端
// 自己写的值，按 IP 的限流与登录锁定就形同虚设。这里把默认改为
// 「谁都不信」：trustedProxies 为空时 ClientIP 只取 TCP 直连地址。
func newEngine(trustedProxies []string) (*gin.Engine, error) {
	r := gin.New()
	// 传 nil 表示不信任任何代理；空切片同样处理，避免 Gin 内部的歧义。
	var proxies []string
	if len(trustedProxies) > 0 {
		proxies = trustedProxies
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return nil, fmt.Errorf("server.trusted_proxies 无效: %w", err)
	}
	return r, nil
}

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

	r, err := newEngine(cfg.Server.TrustedProxies)
	if err != nil {
		return err
	}

	// 全局中间件
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())
	// 安全响应头：nosniff + 上传目录的 SVG/PDF 加固
	r.Use(middleware.SecurityHeaders())

	// 静态文件
	r.Static("/static", "./web/static")

	// 注册路由
	registerRoutes(r)

	// Swagger 文档路由
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	registerSPAFallback(r)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := newHTTPServer(addr, r)

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
