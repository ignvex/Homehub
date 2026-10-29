// Package middleware 提供 HomeHub HTTP 中间件。
//
// 包含：
//   - Logger：请求日志记录
//   - AuthRequired：会话认证校验
//   - sessionCache：会话用户内存缓存
package middleware

import (
	"log"
	"net/http"
	"sync"
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
	// sessionCacheTTL 缓存有效期（设为 5 分钟，确保密码修改/禁用在 5 分钟内生效）。
	sessionCacheTTL = 5 * time.Minute
)

// ============================================================
// Session 内存缓存 — 减少高频页面访问时的 DB 查询
// ============================================================

type sessionCacheEntry struct {
	user      *model.User
	expiresAt time.Time
}

var (
	sessionCache   = make(map[string]sessionCacheEntry)
	sessionCacheMu sync.RWMutex
)

// getCachedUser 从缓存读取已认证用户，cache hit 时跳过 DB 查询。
// 返回 nil 表示缓存未命中或已过期。
func getCachedUser(token string) *model.User {
	sessionCacheMu.RLock()
	entry, ok := sessionCache[token]
	sessionCacheMu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.user
}

// setCachedUser 将用户写入缓存，设置 TTL 过期时间。
func setCachedUser(token string, user *model.User) {
	sessionCacheMu.Lock()
	sessionCache[token] = sessionCacheEntry{user: user, expiresAt: time.Now().Add(sessionCacheTTL)}
	sessionCacheMu.Unlock()
}

// InvalidateSession 清除指定 token 的缓存。
//
// 供 handler 和 service 层在以下场景调用：
//   - 用户登出
//   - 修改密码（强制所有会话重新验证）
//   - 管理员禁用用户
func InvalidateSession(token string) {
	sessionCacheMu.Lock()
	delete(sessionCache, token)
	sessionCacheMu.Unlock()
}

// InvalidateSessionForUser 清除指定用户所有 session 的缓存。
func InvalidateSessionForUser(userID uint64) {
	sessionCacheMu.Lock()
	for token, entry := range sessionCache {
		if entry.user != nil && entry.user.ID == userID {
			delete(sessionCache, token)
		}
	}
	sessionCacheMu.Unlock()
}

// ============================================================
// 中间件
// ============================================================

// AuthRequired 返回一个 Gin 中间件，校验请求携带的会话 Cookie 是否有效。
//
// 校验流程（含缓存）：
//  1. 从 Cookie 中读取 session token
//  2. 查询内存缓存（5 分钟 TTL），命中则跳过 DB
//  3. 查询数据库验证 session + user
//  4. 验证通过后写入缓存
//  5. 将 *model.User 注入 gin.Context（键："user"）
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

		// 缓存快速路径
		if user := getCachedUser(token); user != nil {
			c.Set("user", user)
			c.Set("session_token", token)
			c.Next()
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
			// 清理过期 session + 缓存
			if delErr := db.Delete(&session).Error; delErr != nil {
				log.Printf("[auth] 清理过期会话失败: %v", delErr)
			}
			InvalidateSession(token)
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

		setCachedUser(token, &user)
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

		// 缓存快速路径
		if user := getCachedUser(token); user != nil {
			c.Set("user", user)
			c.Set("session_token", token)
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
			if delErr := db.Delete(&session).Error; delErr != nil {
				log.Printf("[auth] 清理过期会话失败: %v", delErr)
			}
			InvalidateSession(token)
			c.Next()
			return
		}

		var user model.User
		if err := db.First(&user, session.UserID).Error; err != nil {
			c.Next()
			return
		}

		setCachedUser(token, &user)
		c.Set("user", &user)
		c.Set("session_token", token)
		c.Next()
	}
}

// AuthAdminRequired 返回一个 Gin 中间件，校验已登录用户是否为管理员。
//
// 需在 AuthRequired 之后使用（即路由需先通过认证中间件）。
// 若当前用户 role 不是 "admin"，返回 403 Forbidden。
func AuthAdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, exists := c.Get("user")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 30001, "message": "权限不足，需要管理员权限",
			})
			return
		}

		user := u.(*model.User)
		if user.Role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 30001, "message": "权限不足，需要管理员权限",
			})
			return
		}

		c.Next()
	}
}
