package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/middleware"
	"github.com/ignvex/HomeHub/internal/model"
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

	// CSRF 防护：对非安全方法校验 Origin/Referer 与双提交 cookie。
	// 挂在 API 根组上，所有子路由自动生效；静态资源与 SPA 不受影响。
	api.Use(middleware.CSRFProtect())

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

		// ---- 内容写操作：3 级（editor）及以上 ----
		//
		// 3 级可以管全部内容（不分作者）；逐条归属的校验在 service 层做。
		contentWrite := authRequired.Group("")
		contentWrite.Use(middleware.AuthLevelRequired(model.LevelEditor))

		// 书签分组（写操作）
		bg := contentWrite.Group("/bookmark-groups")
		{
			bg.POST("", handler.BookmarkGroupCreate)
			bg.PUT("/:id", handler.BookmarkGroupUpdate)
			bg.DELETE("/:id", handler.BookmarkGroupDelete)
			bg.POST("/reorder", handler.BookmarkGroupReorder)
		}

		// 书签（写操作）
		b := contentWrite.Group("/bookmarks")
		{
			b.POST("", handler.BookmarkCreate)
			b.PUT("/:id", handler.BookmarkUpdate)
			b.DELETE("/:id", handler.BookmarkDelete)
			b.POST("/reorder", handler.BookmarkReorder)
		}

		// 配置管理（管理员）——可改数据库连接与 session_secret，必须限制角色
		cfg := authRequired.Group("/config")
		cfg.Use(middleware.AuthAdminRequired())
		{
			cfg.GET("", handler.ConfigGet)
			cfg.PUT("", handler.ConfigUpdate)
		}

		// 媒体资源：读需登录；上传/删除需 4 级（member）及以上，
		// 且只能动自己上传的（owner_id 校验在 service 层）。
		mediaRead := authRequired.Group("/media-assets")
		{
			mediaRead.GET("", handler.MediaList)
		}
		mediaWrite := authRequired.Group("/media-assets")
		mediaWrite.Use(middleware.AuthLevelRequired(model.LevelMember))
		{
			mediaWrite.POST("", handler.MediaUpload)
			mediaWrite.DELETE("/:id", handler.MediaDelete)
		}

		// 背景设置（内容配置，3 级）
		bgCfg := contentWrite.Group("/background")
		{
			bgCfg.GET("", handler.BackgroundGet)
			bgCfg.PUT("", handler.BackgroundUpdate)
		}

		// RSS 订阅：读操作只需登录，写操作要求 3 级
		rssRead := authRequired.Group("/rss")
		{
			rssRead.GET("/feeds", handler.RSSListFeeds)
			rssRead.GET("/feeds/:id", handler.RSSGetFeed)
			rssRead.GET("/entries", handler.RSSListEntries)
			rssRead.GET("/groups", handler.RSSListGroups)
			rssRead.GET("/groups/:id", handler.RSSGetGroup)
			rssRead.GET("/pikpak", handler.RSSPikPakStatus)
		}

		rssWrite := contentWrite.Group("/rss")
		{
			rssWrite.POST("/feeds", handler.RSSCreateFeed)
			rssWrite.PUT("/feeds/:id", handler.RSSUpdateFeed)
			rssWrite.DELETE("/feeds/:id", handler.RSSDeleteFeed)
			rssWrite.POST("/feeds/:id/fetch", handler.RSSFetchFeed)
			rssWrite.POST("/fetch-all", handler.RSSFetchAll)
			rssWrite.POST("/groups", handler.RSSCreateGroup)
			rssWrite.PUT("/groups/:id", handler.RSSUpdateGroup)
			rssWrite.DELETE("/groups/:id", handler.RSSDeleteGroup)

			// 磁力链接锁定与 PikPak 提交
			rssWrite.PUT("/entries/:id/magnet/lock", handler.RSSLockMagnet)
			rssWrite.DELETE("/entries/:id/magnet/lock", handler.RSSUnlockMagnet)
			rssWrite.POST("/entries/:id/magnet/pikpak", handler.RSSSubmitMagnet)
		}

		// 余额监控
		balance := authRequired.Group("/balances")
		{
			balance.GET("", handler.BalanceList)
			balance.GET("/status", handler.BalanceStatus)
			balance.POST("/refresh", handler.BalanceRefresh)
		}

		// 监控点的增删改属于内容写（与 RSS/书签一致）。
		// api_key 只进不出：写接口收它，读接口一律只回掩码。
		balanceWrite := contentWrite.Group("/balances")
		{
			balanceWrite.POST("", handler.BalanceCreate)
			balanceWrite.PUT("/:id", handler.BalanceUpdate)
			balanceWrite.DELETE("/:id", handler.BalanceDelete)
			balanceWrite.POST("/:id/refresh", handler.BalanceRefreshOne)
		}

		// ---- Extension hub 通道 ----
		//
		// 两类权限分开：镜像订阅是内容写操作（3 级，与 RSS 写一致）；
		// 签发密钥等同于发账号，需要 2 级；删除对方已抓取的内容不可逆，
		// 也抬到 2 级，不跟内容写共用一个门槛。
		extHubRead := authRequired.Group("/extension")
		{
			extHubRead.GET("/status", handler.ExtensionHubStatus)
			extHubRead.GET("/subscriptions", handler.ExtensionHubListSubscriptions)
		}

		extHubPush := contentWrite.Group("/extension")
		{
			extHubPush.POST("/subscriptions", handler.ExtensionHubPushSubscription)
			extHubPush.POST("/subscriptions/sync", handler.ExtensionHubSync)
		}

		extHubOps := authRequired.Group("/extension")
		extHubOps.Use(middleware.AuthLevelRequired(model.LevelOperator))
		{
			extHubOps.DELETE("/subscriptions/:id", handler.ExtensionHubDeleteSubscription)
			extHubOps.GET("/keys", handler.ExtensionHubListKeys)
			extHubOps.POST("/keys", handler.ExtensionHubIssueKey)
			extHubOps.DELETE("/keys/:id", handler.ExtensionHubRevokeKey)
		}

		// TODO 待办：读需登录，写需 3 级
		todoRead := authRequired.Group("/todos")
		{
			todoRead.GET("", handler.TodoList)
			todoRead.GET("/:id", handler.TodoGet)
		}
		todoWrite := contentWrite.Group("/todos")
		{
			todoWrite.POST("", handler.TodoCreate)
			todoWrite.PUT("/:id", handler.TodoUpdate)
			todoWrite.DELETE("/:id", handler.TodoDelete)
			todoWrite.PATCH("/:id/status", handler.TodoUpdateStatus)
			todoWrite.POST("/reorder", handler.TodoReorder)
		}

		// 用户管理与日志：2 级（operator）及以上。
		// 具体到“能不能动某个账号”，service 层会再比一次等级。
		admin := authRequired.Group("/admin")
		admin.Use(middleware.AuthLevelRequired(model.LevelOperator))
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
				adminLogs.DELETE("", handler.LogClearAll)
				adminLogs.GET("/stream", handler.LogStream)
			}
		}
	}
}
