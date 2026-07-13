package handler

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/middleware"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
)

// 登录限速：记录每个用户名的失败尝试次数和时间戳。
var (
	loginAttemptsMu sync.RWMutex
	loginAttempts   = make(map[string]*loginAttempt)
	loginMaxRetries = 5
	loginCooldown   = 15 * time.Minute
)

// loginAttempt 登录尝试记录，由 loginAttemptsMu 保护并发访问。
type loginAttempt struct {
	count    int
	lockedAt time.Time
}

// ============================================================
// 认证 API
// ============================================================

// loginRequest 登录请求体。
type loginRequest struct {
	Username string `json:"username" form:"username" binding:"required"`
	Password string `json:"password" form:"password" binding:"required"`
}

// AuthLogin 处理用户登录。
//
//	@Summary		用户登录
//	@Description	验证用户名和密码，成功时设置 session cookie 并返回 token
//	@Tags			认证
//	@Accept			json
//	@Produce		json
//	@Param			request	body		loginRequest	true	"登录请求"
//	@Success		200		{object}	Response{data=object}	"登录成功，返回 token"
//	@Failure		400		{object}	Response	"请求参数错误"
//	@Failure		401		{object}	Response	"用户名或密码错误"
//	@Router			/auth/login [post]
func AuthLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供用户名和密码")
		return
	}

	// 登录限速：检查是否被锁定
	loginAttemptsMu.RLock()
	att, locked := loginAttempts[req.Username]
	loginAttemptsMu.RUnlock()
	if locked && att.count >= loginMaxRetries {
		if time.Since(att.lockedAt) < loginCooldown {
			Fail(c, http.StatusTooManyRequests, CodeAuthFailed, "登录尝试过多，请15分钟后再试")
			return
		}
		// 冷却时间已过，重置
		loginAttemptsMu.Lock()
		delete(loginAttempts, req.Username)
		loginAttemptsMu.Unlock()
	}

	token, err := svc.Auth.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrAuthFailed) {
			// 记录失败尝试（加锁保护）
			loginAttemptsMu.Lock()
			a := loginAttempts[req.Username]
			if a == nil {
				a = &loginAttempt{}
				loginAttempts[req.Username] = a
			}
			a.count++
			if a.count >= loginMaxRetries {
				a.lockedAt = time.Now()
			}
			loginAttemptsMu.Unlock()
			Fail(c, http.StatusUnauthorized, CodeAuthFailed, "用户名或密码错误")
			return
		}
		if errors.Is(err, service.ErrUserDisabled) {
			Fail(c, http.StatusForbidden, CodeUserDisabled, "账号已被禁用，请联系管理员")
			return
		}
		FailInternal(c, "登录失败")
		return
	}
	// 登录成功，清除限速记录
	loginAttemptsMu.Lock()
	delete(loginAttempts, req.Username)
	loginAttemptsMu.Unlock()

	// 设置 Cookie
	c.SetCookie(
		middleware.SessionCookieName,
		token,
		int(middleware.SessionDuration.Seconds()),
		"/",
		"",
		false, // Secure: false（本地部署不用 HTTPS）
		true,  // HttpOnly
	)

	// 通知 htmx 客户端跳转到首页（非 htmx 调用者照常解析 JSON body）
	c.Header("HX-Redirect", "/")

	Success(c, gin.H{"token": token})
}

// AuthLogout 处理用户登出。
//
//	@Summary	用户登出
//	@Description	清除 session cookie 并注销
//	@Tags		认证
//	@Produce	json
//	@Success	200	{object}	Response	"登出成功"
//	@Router		/auth/logout [post]
func AuthLogout(c *gin.Context) {
	token, exists := c.Get("session_token")
	if exists {
		if tokenStr, ok := token.(string); ok {
			_ = svc.Auth.Logout(tokenStr)
		}
	}

	// 清除 Cookie
	c.SetCookie(
		middleware.SessionCookieName,
		"",
		-1,
		"/",
		"",
		false,
		true,
	)

	Success(c, nil)
}

// AuthStatus 返回当前登录状态。
//
//	@Summary	获取登录状态
//	@Description	返回当前用户是否已登录
//	@Tags		认证
//	@Produce	json
//	@Success	200	{object}	Response{data=object}	"返回登录状态"
//	@Router		/auth/status [get]
func AuthStatus(c *gin.Context) {
	// 从 Cookie 获取 token 并尝试验证
	token, err := c.Cookie(middleware.SessionCookieName)
	if err != nil || token == "" {
		c.JSON(http.StatusOK, gin.H{
			"logged_in": false,
		})
		return
	}

	// 完整校验由中间件完成
	u, exists := c.Get("user")
	if exists {
		user := u.(*model.User)
		c.JSON(http.StatusOK, gin.H{
			"logged_in":  true,
			"username":   user.Username,
			"role":       user.Role,
			"email":      user.Email,
			"avatar_url": buildAvatarURL(user.ID),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"logged_in": true,
	})

}

// changePasswordRequest 修改密码请求体。
type changePasswordRequest struct {
	OldPassword string `json:"old_password" form:"old_password" binding:"required"`
	NewPassword string `json:"new_password" form:"new_password" binding:"required"`
}

// AuthChangePassword 修改密码。
//
//	@Summary		修改密码
//	@Description	验证旧密码后修改为新密码，成功后清除所有会话
//	@Tags			认证
//	@Accept			json
//	@Produce		json
//	@Param			request	body		changePasswordRequest	true	"修改密码请求"
//	@Success		200		{object}	Response	"修改成功"
//	@Failure		400		{object}	Response	"请求参数错误或旧密码错误"
//	@Failure		401		{object}	Response	"未登录"
//	@Router			/auth/change-password [post]
func AuthChangePassword(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists {
		FailUnauthorized(c, "未登录")
		return
	}
	user := u.(*model.User)

	var req changePasswordRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供旧密码和新密码")
		return
	}

	if err := svc.Auth.ChangePassword(user.ID, req.OldPassword, req.NewPassword); err != nil {
		if errors.Is(err, service.ErrAuthFailed) {
			Fail(c, http.StatusBadRequest, CodeAuthFailed, "旧密码错误")
			return
		}
		if errors.Is(err, service.ErrPasswordTooWeak) {
			Fail(c, http.StatusBadRequest, CodePasswordTooWeak, "新密码长度不能少于 6 位")
			return
		}
		FailInternal(c, "修改密码失败")
		return
	}

	// 修改密码后清除 Cookie
	c.SetCookie(
		middleware.SessionCookieName,
		"",
		-1,
		"/",
		"",
		false,
		true,
	)

	Success(c, nil)
}

// ============================================================
// Profile API
// ============================================================

// updateProfileRequest 更新个人资料请求体。
type updateProfileRequest struct {
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
}

// AuthGetProfile 获取当前登录用户的个人信息。
//
// GET /api/v1/auth/profile
func AuthGetProfile(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists {
		FailUnauthorized(c, "未登录")
		return
	}
	user := u.(*model.User)

	Success(c, gin.H{
		"id":         user.ID,
		"username":   user.Username,
		"email":      user.Email,
		"role":       user.Role,
		"avatar_url": buildAvatarURL(user.ID),
	})
}

// AuthUpdateProfile 当前用户更新自己的个人资料。
//
// PUT /api/v1/auth/profile
func AuthUpdateProfile(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists {
		FailUnauthorized(c, "未登录")
		return
	}
	user := u.(*model.User)

	var req updateProfileRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的参数")
		return
	}

	svcReq := service.UpdateUserRequest{
		Username: req.Username,
		Email:    req.Email,
	}

	updated, err := svc.Auth.UpdateUser(user.ID, user.ID, svcReq)
	if err != nil {
		if errors.Is(err, service.ErrUsernameExists) {
			Fail(c, http.StatusConflict, CodeUsernameExists, "用户名已存在")
			return
		}
		FailInternal(c, "更新资料失败")
		return
	}

	Success(c, gin.H{
		"id":         updated.ID,
		"username":   updated.Username,
		"email":      updated.Email,
		"role":       updated.Role,
		"avatar_url": buildAvatarURL(updated.ID),
	})
}
