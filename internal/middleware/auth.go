// Package middleware 提供 HomeHub HTTP 中间件。
//
// 包含：
//   - Logger：请求日志记录
//   - AuthRequired：会话认证校验
//   - sessionCache：会话用户内存缓存
package middleware

import (
	"fmt"
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
	SessionCookieName = "session"
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
	// sessionCacheLastSweep 上次回收过期条目的时间，由 sessionCacheMu 保护。
	sessionCacheLastSweep time.Time
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
//
// 写入时顺带回收过期条目（每个 TTL 周期至多一次）：过期条目只会在同一
// token 再次访问时被覆盖，换浏览器、清 cookie 留下的条目永远不会再被读，
// 不回收的话 map 只增不减。写路径本来就持写锁，摊销成本可忽略。
func setCachedUser(token string, user *model.User) {
	now := time.Now()
	sessionCacheMu.Lock()
	if now.Sub(sessionCacheLastSweep) >= sessionCacheTTL {
		sessionCacheLastSweep = now
		for k, e := range sessionCache {
			if now.After(e.expiresAt) {
				delete(sessionCache, k)
			}
		}
	}
	sessionCache[token] = sessionCacheEntry{user: user, expiresAt: now.Add(sessionCacheTTL)}
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
			// 账号已停用时不得放行。缓存只是一个快照，不能因为
			// "上次校验通过了"就绕过停用。
			if !user.IsActive {
				InvalidateSession(token)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					// 20004 = handler.CodeUserDisabled。middleware 不能
					// import handler（handler 反向依赖 middleware，会成环），
					// 所以只能硬编码，改动时需与 response.go 的码表同步。
					"code": 20004, "message": "账号已被停用",
				})
				return
			}
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

		// 停用的账号立即失效：删除其余会话、清缓存、拒绝本次请求。
		//
		// 这里必须校验：登录时只查一次 is_active，会话本身不带启用状态。
		// 不在这里拦，被停用的人可以继续用旧 cookie 访问全部接口，
		// 直到会话自然过期。
		if !user.IsActive {
			if delErr := db.Unscoped().Where("user_id = ?", user.ID).
				Delete(&model.Session{}).Error; delErr != nil {
				log.Printf("[auth] 清理停用用户会话失败: %v", delErr)
			}
			InvalidateSessionForUser(user.ID)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				// 20004 = handler.CodeUserDisabled，与登录路径保持一致。
				"code": 20004, "message": "账号已被停用",
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
			// 停用账号不被当作已登录：否则前端会渲染出
			// "已登录但所有接口 401" 的矛盾页面。
			if !user.IsActive {
				InvalidateSession(token)
				c.Next()
				return
			}
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

		if !user.IsActive {
			InvalidateSession(token)
			c.Next()
			return
		}

		setCachedUser(token, &user)
		c.Set("user", &user)
		c.Set("session_token", token)
		c.Next()
	}
}

// AuthLevelRequired 返回一个按等级授权的中间件。
//
// 需在 AuthRequired 之后使用。语义是“至少这么高”：传 LevelEditor (3)
// 会放行 1–3 级。数值小 = 权限高，所以比较是 `user.Level <= required`。
//
// 注意：这里只做**等级门槛**判断，不管“这条数据是不是他的”。
// 涉及归属的地方（如删除别人上传的媒体）要在 handler / service 里
// 用 user.CanManage 或 owner_id 再判一次。
func AuthLevelRequired(required int) gin.HandlerFunc {
	required = model.NormalizeLevel(required)
	name := model.LevelName(required)

	return func(c *gin.Context) {
		u, exists := c.Get("user")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    30001,
				"message": fmt.Sprintf("权限不足，需要 %d 级（%s）或更高", required, name),
			})
			return
		}

		user, ok := u.(*model.User)
		if !ok || !user.HasLevel(required) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    30001,
				"message": fmt.Sprintf("权限不足，需要 %d 级（%s）或更高", required, name),
			})
			return
		}

		c.Next()
	}
}

// AuthAdminRequired 是 AuthLevelRequired(LevelAdmin) 的语义化别名。
//
// 保留它是因为“管理员专属”在调用处比“1 级及以上”更好读；
// 两者的实现与行为必须完全一致。
func AuthAdminRequired() gin.HandlerFunc {
	return AuthLevelRequired(model.LevelAdmin)
}
