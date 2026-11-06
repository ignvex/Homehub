package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/middleware"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
)

// 登录失败锁定。
//
// 两个计数器，各管一种攻击：
//
//   - 按「用户名 + 客户端 IP」：同一来源对同一账号 15 分钟内失败 5 次，
//     锁定这个组合 15 分钟。**只按用户名锁定**会让任何人在任何地方
//     输错 5 次 admin 的密码就把真正的管理员锁在门外；绑定 IP 后，
//     攻击者只能锁住他自己。
//   - 按客户端 IP：同一来源 15 分钟内累计失败 20 次（不论用户名），
//     锁定这个 IP。挡的是「每个用户名只试几次」的撞库。
//
// 分布式（大量 IP）爆破不在这里处理：单实例内存计数做不到，
// 请求速率另有路由层的按 IP 令牌桶。
// IP 取 c.ClientIP()，反向代理部署需配置 Gin 的可信代理。
var (
	loginPairFailures = middleware.NewFailureLimiter(5, 15*time.Minute)
	loginIPFailures   = middleware.NewFailureLimiter(20, 15*time.Minute)
)

// loginPairKey 组合用户名与 IP。用 NUL 分隔：两者都不会含 NUL，
// 不会出现 ("a|b","c") 与 ("a","b|c") 撞键。
func loginPairKey(username, ip string) string {
	return username + "\x00" + ip
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
//	@Failure		429		{object}	Response	"登录尝试过多"
//	@Router			/auth/login [post]
func AuthLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供用户名和密码")
		return
	}

	ip := c.ClientIP()
	pairKey := loginPairKey(req.Username, ip)

	// 先查锁定再碰数据库：锁定期间连 bcrypt 都不跑。
	if locked, _ := loginIPFailures.Locked(ip); locked {
		Fail(c, http.StatusTooManyRequests, CodeAuthFailed, "登录尝试过多，请15分钟后再试")
		return
	}
	if locked, _ := loginPairFailures.Locked(pairKey); locked {
		Fail(c, http.StatusTooManyRequests, CodeAuthFailed, "登录尝试过多，请15分钟后再试")
		return
	}

	token, err := svc.Auth.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrAuthFailed) {
			loginPairFailures.Fail(pairKey)
			loginIPFailures.Fail(ip)
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
	// 登录成功只清这个组合的记录；IP 级计数照旧，避免攻击者用
	// 自己的账号登录一次就把撞库计数清零。
	loginPairFailures.Reset(pairKey)

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
			middleware.InvalidateSession(tokenStr)
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
			"id":         user.ID,
			"username":   user.Username,
			"role":       model.LevelName(user.Level),
			"level":      user.Level,
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

	// 修改密码后清除缓存 + Cookie
	if token, exists := c.Get("session_token"); exists {
		if tokenStr, ok := token.(string); ok {
			middleware.InvalidateSession(tokenStr)
		}
	}
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
//	@Summary		获取个人资料
//	@Description	返回当前登录用户的用户名、邮箱、角色和头像 URL
//	@Tags			认证
//	@Produce		json
//	@Success		200	{object}	Response	"用户个人信息"
//	@Failure		401	{object}	Response	"未登录"
//	@Router			/auth/profile [get]
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
		"role":       model.LevelName(user.Level),
		"level":      user.Level,
		"avatar_url": buildAvatarURL(user.ID),
	})
}

// AuthUpdateProfile 当前用户更新自己的个人资料。
//
//	@Summary		更新个人资料
//	@Description	当前登录用户修改自己的用户名或邮箱
//	@Tags			认证
//	@Accept			json
//	@Produce		json
//	@Param			request	body		updateProfileRequest	true	"更新请求"
//	@Success		200		{object}	Response				"更新后的用户信息"
//	@Failure		400		{object}	Response				"请求参数错误"
//	@Failure		401		{object}	Response				"未登录"
//	@Failure		409		{object}	Response				"用户名已存在"
//	@Router			/auth/profile [put]
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

	updated, err := svc.Auth.UpdateUser(user.ID, user, svcReq)
	if err != nil {
		if errors.Is(err, service.ErrUsernameExists) {
			Fail(c, http.StatusConflict, CodeUsernameExists, "用户名已存在")
			return
		}
		if errors.Is(err, service.ErrEmailExists) {
			Fail(c, http.StatusConflict, CodeEmailExists, err.Error())
			return
		}
		if errors.Is(err, service.ErrInvalidEmail) {
			FailBadRequest(c, err.Error())
			return
		}
		// 其余错误同样带上原因：笼统的「更新资料失败」让用户无从下手。
		FailInternal(c, "更新资料失败："+err.Error())
		return
	}

	// 清除当前 session 缓存，确保下次请求读到最新用户名
	if token, ok := c.Get("session_token"); ok {
		middleware.InvalidateSession(token.(string))
	}

	Success(c, gin.H{
		"id":         updated.ID,
		"username":   updated.Username,
		"email":      updated.Email,
		"role":       model.LevelName(updated.Level),
		"level":      updated.Level,
		"avatar_url": buildAvatarURL(updated.ID),
	})
}
