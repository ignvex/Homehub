package server

import (
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/middleware"
)

// registerRoutes 向 Gin 引擎注册所有路由。
//
// 路由分为两组：
//   - 页面路由（/login, /, /bookmarks, /settings）：服务端渲染 HTML 页面
//   - API 路由（/api/v1/...）：JSON 接口，多数需要认证
func registerRoutes(r *gin.Engine) {
	// ============================================================
	// 页面路由（公开，可选认证以便模板知晓登录状态）
	// ============================================================
	r.GET("/login", handler.LoginPage)
	r.GET("/", middleware.AuthOptional(), handler.HomePage)
	r.GET("/bookmarks", middleware.AuthOptional(), handler.BookmarksPage)
	r.GET("/settings", middleware.AuthOptional(), handler.SettingsPage)

	// ============================================================
	// API v1
	// ============================================================
	api := r.Group("/api/v1")

	// --- 认证（无需登录） ---
	auth := api.Group("/auth")
	{
		auth.POST("/login", handler.AuthLogin)
		auth.GET("/status", handler.AuthStatus)
	}

	// --- 只读 API（公开，可选认证） ---
	publicAPI := api.Group("")
	publicAPI.Use(middleware.AuthOptional())
	{
		publicAPI.GET("/bookmarks", handler.BookmarkList)
		publicAPI.GET("/bookmark-groups", handler.BookmarkGroupList)
	}

	// --- 以下路由需要认证 ---
	authRequired := api.Group("")
	authRequired.Use(middleware.AuthRequired())
	{
		// 登出
		authRequired.POST("/auth/logout", handler.AuthLogout)
		authRequired.POST("/auth/change-password", handler.AuthChangePassword)

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
	}
}
