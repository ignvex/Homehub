package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/middleware"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 认证 API
// ============================================================

// loginRequest 登录请求体。
type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// AuthLogin 处理用户登录。
//
// POST /api/v1/auth/login
func AuthLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请提供用户名和密码")
		return
	}

	token, err := svc.Auth.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrAuthFailed) {
			Fail(c, http.StatusUnauthorized, CodeAuthFailed, "用户名或密码错误")
			return
		}
		FailInternal(c, "登录失败")
		return
	}

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

	Success(c, gin.H{"token": token})
}

// AuthLogout 处理用户登出。
//
// POST /api/v1/auth/logout
func AuthLogout(c *gin.Context) {
	token, _ := c.Get("session_token")
	if tokenStr, ok := token.(string); ok {
		_ = svc.Auth.Logout(tokenStr)
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
// GET /api/v1/auth/status
func AuthStatus(c *gin.Context) {
	// 从 Cookie 获取 token 并尝试验证
	token, err := c.Cookie(middleware.SessionCookieName)
	if err != nil || token == "" {
		c.JSON(http.StatusOK, gin.H{
			"logged_in": false,
		})
		return
	}

	// 简单判断：如果有 cookie 就视为已登录（完整校验由中间件完成）
	u, exists := c.Get("user")
	if exists {
		user := u.(*model.User)
		c.JSON(http.StatusOK, gin.H{
			"logged_in": true,
			"username":  user.Username,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"logged_in": true,
	})

	// 消除 unused 警告
	_ = token
}

// changePasswordRequest 修改密码请求体。
type changePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// AuthChangePassword 修改密码。
//
// POST /api/v1/auth/change-password
func AuthChangePassword(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists {
		FailUnauthorized(c, "未登录")
		return
	}
	user := u.(*model.User)

	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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
