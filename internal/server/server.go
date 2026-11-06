// Package server 提供 HomeHub HTTP 服务的启动、路由注册与优雅关闭。
//
// 基于 Gin 框架，挂载全局中间件（recovery / logger / auth），
// 注册 API 路由与页面路由，提供信号驱动的优雅关闭。
package server

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/http"
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

// shutdownTimeout 是等待在途请求完成的上限，超时后强制断开剩余连接。
//
// 与 app 的后台任务等待（10s）相加仍要落在容器的停止宽限期内，
// 否则 Docker 会在收尾途中发 SIGKILL。compose 里把宽限期设为 30s。
const shutdownTimeout = 5 * time.Second

// Run 启动 HTTP 服务并阻塞，直至 ctx 取消后优雅关闭，或监听失败。
//
// 信号处理不在这里：app.Run 在启动最开始就把 SIGINT/SIGTERM 接到 ctx 上，
// 这样启动阶段（例如等待数据库就绪）收到信号也能及时退出。
//
// 启动序列：
//  1. 根据配置设置 Gin 运行模式
//  2. 创建 Gin 引擎
//  3. 挂载全局中间件
//  4. 加载模板与静态文件
//  5. 注册所有路由
//  6. 同步绑定端口，再在后台 Serve
//  7. 等待 ctx 取消，触发优雅关闭（超时则强制关闭）
func Run(ctx context.Context) error {
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

	// 先同步绑定端口。之前 ListenAndServe 放在 goroutine 里，端口被占用时
	// 只记一条错误日志，进程却继续阻塞等信号：容器显示 running、健康检查
	// 一直失败，restart 策略也不会介入。现在绑定失败直接返回，进程以非零
	// 码退出，交给容器的重启策略处理。
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	return serve(ctx, srv, ln, shutdownTimeout)
}

// serve 在 ln 上提供服务，直到 ctx 取消或 Serve 出错。
//
// 拆出来是为了在测试里用临时端口驱动完整的关闭流程。
func serve(ctx context.Context, srv *http.Server, ln net.Listener, timeout time.Duration) error {
	// Shutdown 只等空闲连接，SSE 流永远不空闲：不主动断开，每次关闭都会
	// 等满超时再被强制 Close，赶上容器的停止宽限期还可能被 SIGKILL。
	// 所有请求的 context 都派生自 baseCtx，Shutdown 开始时取消它，
	// 长连接处理器从 c.Request.Context() 上就能看到并自行返回。
	// 按 server 实例持有，而不是包级一次性开关，重复启动互不影响。
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv.BaseContext = func(net.Listener) context.Context { return baseCtx }
	srv.RegisterOnShutdown(cancelBase)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务启动", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		// Serve 只会因为错误提前返回（没人调用过 Shutdown）。
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-ctx.Done():
	}

	logger.Info("收到终止信号，开始优雅关闭...", "timeout", timeout.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		// 超时仍有请求未完成：强制断开，而不是把错误返回后让连接悬着。
		// 关闭流程照常继续，后台任务和数据库还要按顺序收尾。
		logger.Warn("优雅关闭超时，强制断开剩余连接", "error", err)
		_ = srv.Close()
	}
	<-errCh // 等 Serve 返回 ErrServerClosed，确保不再接受新请求

	logger.Info("HTTP 服务已关闭")
	return nil
}
