package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/middleware"
	"github.com/ignvex/HomeHub/internal/model"
)

// 静态资源的缓存策略。
//
// 两档，依据是文件名是否带内容哈希：
//
//   - `web/dist/assets/*` 由 vite 产出，文件名里含内容哈希（如
//     `index-D3emB2dI.js`），内容一变文件名就变。它们可以永久缓存——
//     旧文件即使不再被引用也无害，因为新页面指向的是新名字。
//   - `index.html` 引用这些名字，**它自己绝不能被长期缓存**。
//     否则浏览器会拿着一份指向旧 JS 的 index.html 一直不发请求，
//     改完前端刷新页面看到的还是旧代码；而当旧 JS 已被下一次构建
//     删除时，页面直接 404 白屏。
//
// 之前两者都靠浏览器启发式缓存，表现是“改完没生效，要强刷”。
const (
	// assetsCacheControl 用于带内容哈希的构建产物。
	//
	// immutable 告诉浏览器：即使用户按刷新，也不要重新验证这个 URL。
	assetsCacheControl = "public, max-age=31536000, immutable"
	// htmlCacheControl 用于 index.html 与 SPA 回落的 HTML 响应。
	//
	// no-cache 不是“不缓存”，而是“每次使用前都验证”——配合
	// Last-Modified 走 304，代价只是一个请求，换来的是永远拿到
	// 当前版本。
	htmlCacheControl = "no-cache"
)

// staticCacheControl 为请求路径选择缓存策略。
func staticCacheControl(path string) string {
	// 只认 /assets/ 前缀：dist 根目录下的文件（favicon.svg 等）名字
	// 不含哈希，不能当 immutable 缓存。
	if strings.HasPrefix(path, "/assets/") {
		return assetsCacheControl
	}
	return htmlCacheControl
}

func serveSPA(c *gin.Context) {
	c.Header("Cache-Control", htmlCacheControl)
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
			c.Header("Cache-Control", staticCacheControl(path))
			c.File("web/dist" + path)
			return
		}
		serveSPA(c)
	})
}

// 路由级限流器。进程内只有一个路由表，包级变量即可；测试每次
// registerRoutes 都会共享它们，用不同 RemoteAddr 隔离即可。
var (
	// pasteCreateLimiter：每 IP 每分钟 10 次匿名粘贴。
	pasteCreateLimiter = middleware.NewRateLimiter(10, time.Minute)
	// loginRequestLimiter：每 IP 每分钟 30 次登录请求，限制 bcrypt 的 CPU 开销。
	// 失败锁定是另一道闸，在 handler 里。
	loginRequestLimiter = middleware.NewRateLimiter(30, time.Minute)
)

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
		// 每次登录都要跑一次 bcrypt：先按 IP 限请求速率，再由 handler
		// 按失败次数锁定（见 handler/auth.go）。
		auth.POST("/login", middleware.RateLimitByIP(loginRequestLimiter), handler.AuthLogin)
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

		// 剪贴板（公开读写，无需认证）
		publicAPI.GET("/pastes", handler.PasteList)
		// 匿名可写：按 IP 限流，挡住脚本灌库。
		publicAPI.POST("/pastes", middleware.RateLimitByIP(pasteCreateLimiter), handler.PasteCreate)
		publicAPI.GET("/pastes/:id", handler.PasteGet)
		publicAPI.DELETE("/pastes/:id", handler.PasteDelete)
	}

	// --- 以下路由需要认证 ---
	authRequired := api.Group("")
	authRequired.Use(middleware.AuthRequired())
	{
		// 头像（当前用户）
		authRequired.GET("/avatar/me", handler.AvatarGetMe)

		// 搜索：会命中 RSS 条目正文，而 /rss 读接口要求登录，
		// 放在公开组里就成了绕开它的旁路。
		authRequired.GET("/search", handler.SearchAll)

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
			// 正文按需取：列表只带摘要，展开某条时才请求完整 HTML。
			rssRead.GET("/entries/:id/content", handler.RSSEntryContent)
			rssRead.GET("/groups", handler.RSSListGroups)
			rssRead.GET("/groups/:id", handler.RSSGetGroup)
			rssRead.GET("/pikpak", handler.RSSPikPakStatus)
			rssRead.GET("/schedule", handler.RSSSchedule)

			// 阅读状态是个人数据而不是内容写：登录即可，5 级只读用户也能标记。
			rssRead.PUT("/entries/:id/read", handler.RSSMarkEntryRead)
			rssRead.DELETE("/entries/:id/read", handler.RSSMarkEntryUnread)
			rssRead.PUT("/entries/:id/star", handler.RSSStarEntry)
			rssRead.DELETE("/entries/:id/star", handler.RSSUnstarEntry)
			rssRead.POST("/entries/mark-read", handler.RSSMarkEntriesRead)
			rssRead.GET("/unread-counts", handler.RSSUnreadCounts)
		}

		rssWrite := contentWrite.Group("/rss")
		{
			rssWrite.POST("/feeds", handler.RSSCreateFeed)
			// 批量更新放在 :id 之前仅为可读性；路由树里静态段优先于参数段，
			// 两者不会互相遮蔽。
			rssWrite.PUT("/feeds/batch", handler.RSSBatchUpdateFeeds)
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

		// 签名私钥是身份凭据：改它等于换掉整个实例的身份，
		// 所有签名会立刻失效直到 Extension 侧换公钥。这是配置级操作，
		// 抬到 1 级（与 /config 同一道门槛），不跟「签发访问密钥」并列。
		extHubKey := authRequired.Group("/extension")
		extHubKey.Use(middleware.AuthAdminRequired())
		{
			extHubKey.GET("/key", handler.ExtensionHubGetKey)
			extHubKey.PUT("/key", handler.ExtensionHubSetKey)
			extHubKey.DELETE("/key", handler.ExtensionHubDeleteKey)
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
