// Package middleware 提供 HomeHub HTTP 中间件。
//
// 包含：
//   - Logger：请求日志记录
//   - AuthRequired：会话认证校验
package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/database"
	"github.com/ignvex/HomeHub/internal/model"
)

const (
	// SessionCookieName 会话 Cookie 名称。
	SessionCookieName = "homehub_session"
	// SessionDuration 会话有效期。
	SessionDuration = 7 * 24 * time.Hour
)

// AuthRequired 返回一个 Gin 中间件，校验请求携带的会话 Cookie 是否有效。
//
// 校验流程：
//  1. 从 Cookie 中读取 session token
//  2. 查询数据库验证 session 是否存在且未过期
//  3. 查询关联的 User
//  4. 将 *model.User 注入 gin.Context（键："user"）
//
// 校验失败时返回 401 JSON 响应。
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookieName)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 10003, "message": "未登录",
			})
			return
		}

		db := database.DB()
		if db == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code": 10007, "message": "数据库未就绪",
			})
			return
		}

		var session model.Session
		if err := db.Where("token = ?", token).First(&session).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 10003, "message": "会话无效",
			})
			return
		}

		if time.Now().After(session.ExpiresAt) {
			// 清理过期 session
			db.Delete(&session)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 20002, "message": "会话已过期",
			})
			return
		}

		var user model.User
		if err := db.First(&user, session.UserID).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 10003, "message": "用户不存在",
			})
			return
		}

		c.Set("user", &user)
		c.Set("session_token", token)
		c.Next()
	}
}

// AuthOptional 返回一个 Gin 中间件，可选校验会话 Cookie。
//
// 与 AuthRequired 不同，AuthOptional 不会在认证失败时拦截请求。
// 当 session 有效时将 *model.User 注入 gin.Context（键："user"）；
// 无 Cookie 或 session 无效时直接放行，不在 context 中设置 user。
//
// 适用于页面路由和只读 API，让未登录用户也能浏览公开内容。
func AuthOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookieName)
		if err != nil || token == "" {
			c.Next()
			return
		}

		db := database.DB()
		if db == nil {
			c.Next()
			return
		}

		var session model.Session
		if err := db.Where("token = ?", token).First(&session).Error; err != nil {
			c.Next()
			return
		}

		if time.Now().After(session.ExpiresAt) {
			db.Delete(&session)
			c.Next()
			return
		}

		var user model.User
		if err := db.First(&user, session.UserID).Error; err != nil {
			c.Next()
			return
		}

		c.Set("user", &user)
		c.Set("session_token", token)
		c.Next()
	}
}

// AuthPageRequired 返回一个 Gin 中间件，校验会话 Cookie 是否有效。
//
// 与 AuthRequired 共享相同的认证逻辑，但认证失败时执行 302 重定向到
// /login 页面，而非返回 JSON 错误响应。仅用于服务端渲染的页面路由。
func AuthPageRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookieName)
		if err != nil || token == "" {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		db := database.DB()
		if db == nil {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		var session model.Session
		if err := db.Where("token = ?", token).First(&session).Error; err != nil {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		if time.Now().After(session.ExpiresAt) {
			db.Delete(&session)
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		var user model.User
		if err := db.First(&user, session.UserID).Error; err != nil {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		c.Set("user", &user)
		c.Set("session_token", token)
		c.Next()
	}
}
