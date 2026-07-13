package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 用户管理 API（管理员）
// ============================================================

// AdminListUsers 获取所有用户列表。
//
// GET /api/v1/admin/users
//
// @Summary      获取用户列表
// @Description  返回所有注册用户的信息（仅管理员）
// @Tags         用户管理
// @Produce      json
// @Success      200  {object}  Response{data=[]model.User}  "用户列表"
// @Failure      403  {object}  Response  "权限不足"
// @Router       /admin/users [get]
func AdminListUsers(c *gin.Context) {
	users, err := svc.Auth.ListUsers()
	if err != nil {
		FailInternal(c, "查询用户列表失败")
		return
	}
	Success(c, users)
}

// AdminGetUser 获取单个用户详情。
//
// GET /api/v1/admin/users/:id
//
// @Summary      获取用户详情
// @Description  返回指定用户的完整信息（仅管理员）
// @Tags         用户管理
// @Produce      json
// @Param        id  path  int  true  "用户 ID"
// @Success      200  {object}  Response{data=model.User}  "用户详情"
// @Failure      400  {object}  Response  "无效的用户 ID"
// @Failure      403  {object}  Response  "权限不足"
// @Failure      404  {object}  Response  "用户不存在"
// @Router       /admin/users/{id} [get]
func AdminGetUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的用户 ID")
		return
	}

	user, err := svc.Auth.GetUserByID(id)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeUserNotFound, "用户不存在")
			return
		}
		FailInternal(c, "查询用户失败")
		return
	}

	Success(c, user)
}

// createUserRequest 创建用户请求体。
type createUserRequest struct {
	Username string `json:"username" form:"username" binding:"required"`
	Password string `json:"password" form:"password" binding:"required"`
	Email    string `json:"email" form:"email"`
	Role     string `json:"role" form:"role"`
}

// AdminCreateUser 管理员创建新用户。
//
// POST /api/v1/admin/users
//
// @Summary      创建用户
// @Description  管理员创建新用户，可指定角色（admin/user）
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Param        request  body  createUserRequest  true  "用户信息"
// @Success      200  {object}  Response{data=model.User}  "创建成功"
// @Failure      400  {object}  Response  "参数错误或用户名冲突"
// @Failure      403  {object}  Response  "权限不足"
// @Router       /admin/users [post]
func AdminCreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供用户名和密码")
		return
	}

	role := req.Role
	if role == "" {
		role = "user" // 默认角色为普通用户
	}

	user, err := svc.Auth.CreateUser(req.Username, req.Password, role, req.Email)
	if err != nil {
		if errors.Is(err, service.ErrPasswordTooWeak) {
			Fail(c, http.StatusBadRequest, CodePasswordTooWeak, "密码长度不能少于 6 位")
			return
		}
		if errors.Is(err, service.ErrUsernameExists) {
			Fail(c, http.StatusConflict, CodeUsernameExists, "用户名已存在")
			return
		}
		FailBadRequest(c, err.Error())
		return
	}

	Success(c, user)
}

// updateUserRequest 更新用户请求体。
//
// 所有字段均为可选（指针类型），nil 表示不修改。
type updateUserRequest struct {
	Username *string `json:"username,omitempty" form:"username"`
	Email    *string `json:"email,omitempty" form:"email"`
	Role     *string `json:"role,omitempty" form:"role"`
	IsActive *bool   `json:"is_active,omitempty" form:"is_active"`
}

// AdminUpdateUser 管理员更新用户信息。
//
// PUT /api/v1/admin/users/:id
//
// @Summary      更新用户
// @Description  管理员修改用户的用户名、角色或启用状态
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Param        id       path  int                true  "用户 ID"
// @Param        request  body  updateUserRequest  true  "更新字段"
// @Success      200  {object}  Response{data=model.User}  "更新成功"
// @Failure      400  {object}  Response  "参数错误"
// @Failure      403  {object}  Response  "权限不足"
// @Failure      404  {object}  Response  "用户不存在"
// @Failure      409  {object}  Response  "用户名冲突"
// @Router       /admin/users/{id} [put]
func AdminUpdateUser(c *gin.Context) {
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的用户 ID")
		return
	}

	operatorID := getCurrentUserID(c)

	var req updateUserRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的参数")
		return
	}

	svcReq := service.UpdateUserRequest{
		Username: req.Username,
		Email:    req.Email,
		Role:     req.Role,
		IsActive: req.IsActive,
	}

	user, err := svc.Auth.UpdateUser(targetID, operatorID, svcReq)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeUserNotFound, "用户不存在")
			return
		}
		if errors.Is(err, service.ErrUsernameExists) {
			Fail(c, http.StatusConflict, CodeUsernameExists, "用户名已存在")
			return
		}
		if errors.Is(err, service.ErrSelfOperation) {
			Fail(c, http.StatusForbidden, CodeSelfOperation, "不能修改自己的角色或状态")
			return
		}
		FailBadRequest(c, err.Error())
		return
	}

	Success(c, user)
}

// AdminDeleteUser 管理员删除用户。
//
// DELETE /api/v1/admin/users/:id
//
// @Summary      删除用户
// @Description  管理员删除用户及其所有会话
// @Tags         用户管理
// @Produce      json
// @Param        id  path  int  true  "用户 ID"
// @Success      200  {object}  Response  "删除成功"
// @Failure      400  {object}  Response  "无效的用户 ID"
// @Failure      403  {object}  Response  "权限不足或不能删除自己"
// @Failure      404  {object}  Response  "用户不存在"
// @Router       /admin/users/{id} [delete]
func AdminDeleteUser(c *gin.Context) {
	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的用户 ID")
		return
	}

	operatorID := getCurrentUserID(c)

	if err := svc.Auth.DeleteUser(targetID, operatorID); err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeUserNotFound, "用户不存在")
			return
		}
		if errors.Is(err, service.ErrSelfOperation) {
			Fail(c, http.StatusForbidden, CodeSelfOperation, "不能删除自己")
			return
		}
		FailInternal(c, "删除用户失败")
		return
	}

	Success(c, nil)
}

// resetPasswordRequest 重置密码请求体。
type resetPasswordRequest struct {
	NewPassword string `json:"new_password" form:"new_password" binding:"required"`
}

// AdminResetPassword 管理员重置用户密码。
//
// PUT /api/v1/admin/users/:id/reset-password
//
// @Summary      重置用户密码
// @Description  管理员重置指定用户的密码，成功后该用户所有会话失效
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Param        id       path  int                   true  "用户 ID"
// @Param        request  body  resetPasswordRequest  true  "新密码"
// @Success      200  {object}  Response  "重置成功"
// @Failure      400  {object}  Response  "参数错误"
// @Failure      403  {object}  Response  "权限不足"
// @Failure      404  {object}  Response  "用户不存在"
// @Router       /admin/users/{id}/reset-password [put]
func AdminResetPassword(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的用户 ID")
		return
	}

	var req resetPasswordRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供新密码")
		return
	}

	if err := svc.Auth.ResetUserPassword(userID, req.NewPassword); err != nil {
		if errors.Is(err, service.ErrPasswordTooWeak) {
			Fail(c, http.StatusBadRequest, CodePasswordTooWeak, "密码长度不能少于 6 位")
			return
		}
		if errors.Is(err, service.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeUserNotFound, "用户不存在")
			return
		}
		FailInternal(c, "重置密码失败")
		return
	}

	Success(c, nil)
}

// ============================================================
// 辅助函数
// ============================================================

// getCurrentUserID 从 gin.Context 中提取当前登录用户 ID。
func getCurrentUserID(c *gin.Context) uint64 {
	u, exists := c.Get("user")
	if !exists {
		return 0
	}
	user := u.(*model.User)
	return user.ID
}
