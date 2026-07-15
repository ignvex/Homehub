package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/middleware"
)

func serveSPA(c *gin.Context) {
	c.File("web/dist/index.html")
}

func registerSPAFallback(r *gin.Engine) {
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if c.Request.Method != http.MethodGet || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/swagger/") {
			c.Status(http.StatusNotFound)
			return
		}
		// SPA 静态资源：带扩展名的请求从 dist 目录返回（JS/CSS/图片/favicon 等）
		if filepath.Ext(path) != "" {
			c.File("web/dist" + path)
			return
		}
		serveSPA(c)
	})
}

// registerRoutes 向 Gin 引擎注册所有路由。
//
// 路由分为两组：
//   - 页面路由（/login, /, /bookmarks, /settings）：服务端渲染 HTML 页面
//   - API 路由（/api/v1/...）：JSON 接口，多数需要认证
func registerRoutes(r *gin.Engine) {
	// ============================================================
	// 页面路由（由 Vite 前端接管，Go 提供入口文件重定向）
	// ============================================================
	r.GET("/", serveSPA)

	// ============================================================
	// API v1
	// ============================================================
	api := r.Group("/api/v1")

	// --- 健康检查（公开） ---
	api.GET("/health", handler.HealthCheck)
	api.GET("/webui", handler.WebUIConfig)

	// --- 认证（无需登录，AuthOptional 使 status 接口可获得用户信息） ---
	auth := api.Group("/auth")
	auth.Use(middleware.AuthOptional())
	{
		auth.POST("/login", handler.AuthLogin)
		auth.GET("/status", handler.AuthStatus)
	}

	// --- 公开 API ---
	publicAPI := api.Group("")
	publicAPI.Use(middleware.AuthOptional())
	{
		publicAPI.GET("/avatar/:id", handler.AvatarGet)

		// 书签（只读）
		publicAPI.GET("/bookmarks", handler.BookmarkList)
		publicAPI.GET("/bookmark-groups", handler.BookmarkGroupList)

		// 搜索（只读）
		publicAPI.GET("/search", handler.SearchAll)

		// 剪贴板（公开读写，无需认证）
		publicAPI.GET("/pastes", handler.PasteList)
		publicAPI.POST("/pastes", handler.PasteCreate)
		publicAPI.GET("/pastes/:id", handler.PasteGet)
		publicAPI.DELETE("/pastes/:id", handler.PasteDelete)
	}

	// --- 以下路由需要认证 ---
	authRequired := api.Group("")
	authRequired.Use(middleware.AuthRequired())
	{
		// 头像（当前用户）
		authRequired.GET("/avatar/me", handler.AvatarGetMe)

		// 认证
		authRequired.POST("/auth/logout", handler.AuthLogout)
		authRequired.POST("/auth/change-password", handler.AuthChangePassword)

		// 用户 Profile
		authRequired.GET("/auth/profile", handler.AuthGetProfile)
		authRequired.PUT("/auth/profile", handler.AuthUpdateProfile)

		// 书签分组（写操作）
		bg := authRequired.Group("/bookmark-groups")
		{
			bg.POST("", handler.BookmarkGroupCreate)
			bg.PUT("/:id", handler.BookmarkGroupUpdate)
			bg.DELETE("/:id", handler.BookmarkGroupDelete)
			bg.POST("/reorder", handler.BookmarkGroupReorder)
		}

		// 书签（写操作）
		b := authRequired.Group("/bookmarks")
		{
			b.POST("", handler.BookmarkCreate)
			b.PUT("/:id", handler.BookmarkUpdate)
			b.DELETE("/:id", handler.BookmarkDelete)
			b.POST("/reorder", handler.BookmarkReorder)
		}

		// 配置管理
		cfg := authRequired.Group("/config")
		{
			cfg.GET("", handler.ConfigGet)
			cfg.PUT("", handler.ConfigUpdate)
		}

		// 媒体资源
		media := authRequired.Group("/media-assets")
		{
			media.GET("", handler.MediaList)
			media.POST("", handler.MediaUpload)
			media.DELETE("/:id", handler.MediaDelete)
		}

		// 背景设置
		bgCfg := authRequired.Group("/background")
		{
			bgCfg.GET("", handler.BackgroundGet)
			bgCfg.PUT("", handler.BackgroundUpdate)
		}

		// RSS 订阅
		rssGroup := authRequired.Group("/rss")
		{
			rssGroup.GET("/feeds", handler.RSSListFeeds)
			rssGroup.GET("/feeds/:id", handler.RSSGetFeed)
			rssGroup.POST("/feeds", handler.RSSCreateFeed)
			rssGroup.PUT("/feeds/:id", handler.RSSUpdateFeed)
			rssGroup.DELETE("/feeds/:id", handler.RSSDeleteFeed)
			rssGroup.POST("/feeds/:id/fetch", handler.RSSFetchFeed)
			rssGroup.POST("/fetch-all", handler.RSSFetchAll)
			rssGroup.GET("/entries", handler.RSSListEntries)

			// RSS 分组管理
			rssGroup.GET("/groups", handler.RSSListGroups)
			rssGroup.GET("/groups/:id", handler.RSSGetGroup)
			rssGroup.POST("/groups", handler.RSSCreateGroup)
			rssGroup.PUT("/groups/:id", handler.RSSUpdateGroup)
			rssGroup.DELETE("/groups/:id", handler.RSSDeleteGroup)
		}

		// 余额监控
		balance := authRequired.Group("/balances")
		{
			balance.GET("", handler.BalanceList)
			balance.GET("/status", handler.BalanceStatus)
			balance.POST("/refresh", handler.BalanceRefresh)
		}

		// TODO 待办
		todoGroup := authRequired.Group("/todos")
		{
			todoGroup.GET("", handler.TodoList)
			todoGroup.GET("/:id", handler.TodoGet)
			todoGroup.POST("", handler.TodoCreate)
			todoGroup.PUT("/:id", handler.TodoUpdate)
			todoGroup.DELETE("/:id", handler.TodoDelete)
			todoGroup.PATCH("/:id/status", handler.TodoUpdateStatus)
			todoGroup.POST("/reorder", handler.TodoReorder)
		}

		// 用户管理（管理员）
		admin := authRequired.Group("/admin")
		admin.Use(middleware.AuthAdminRequired())
		{
			adminUsers := admin.Group("/users")
			{
				adminUsers.GET("", handler.AdminListUsers)
				adminUsers.POST("", handler.AdminCreateUser)
				adminUsers.GET("/:id", handler.AdminGetUser)
				adminUsers.PUT("/:id", handler.AdminUpdateUser)
				adminUsers.DELETE("/:id", handler.AdminDeleteUser)
				adminUsers.PUT("/:id/reset-password", handler.AdminResetPassword)
				adminUsers.POST("/:id/refresh-avatar", handler.AvatarRefresh)
			}

			// 系统日志（管理员）
			adminLogs := admin.Group("/logs")
			{
				adminLogs.GET("", handler.LogList)
			}
		}
	}
}
