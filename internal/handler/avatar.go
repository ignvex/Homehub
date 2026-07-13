package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
)

// ============================================================
// 头像 API
// ============================================================

// AvatarGet 返回指定用户的头像图片。
//
// GET /api/v1/avatar/:id
//
// 从数据库缓存中读取头像二进制数据。若缓存不存在，自动从 Gravatar 下载并缓存。
//
//	@Summary      获取用户头像
//	@Description  返回缓存的 Gravatar 头像图片
//	@Tags         头像
//	@Produce      image/*
//	@Param        id  path  int  true  "用户 ID"
//	@Success      200  {file}  binary  "头像图片"
//	@Failure      400  {object}  Response  "无效的用户 ID"
//	@Failure      404  {object}  Response  "用户不存在或无头像"
//	@Router       /avatar/{id} [get]
func AvatarGet(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "无效的用户 ID")
		return
	}

	data, contentType, err := svc.Gravatar.GetAvatarData(id)
	if err != nil {
		c.String(http.StatusNotFound, "头像不可用")
		return
	}

	// 写缓存头（30 天）
	c.Header("Cache-Control", "public, max-age=2592000")
	c.Data(http.StatusOK, contentType, data)
}

// AvatarGetMe 返回当前登录用户的头像图片。
//
// GET /api/v1/avatar/me
//
//	@Summary      获取当前用户头像
//	@Description  返回当前登录用户的缓存 Gravatar 头像
//	@Tags         头像
//	@Produce      image/*
//	@Success      200  {file}  binary  "头像图片"
//	@Failure      401  {object}  Response  "未登录"
//	@Router       /avatar/me [get]
func AvatarGetMe(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists {
		FailUnauthorized(c, "未登录")
		return
	}
	user := u.(*model.User)

	data, contentType, err := svc.Gravatar.GetAvatarData(user.ID)
	if err != nil {
		c.String(http.StatusNotFound, "头像不可用")
		return
	}

	c.Header("Cache-Control", "public, max-age=2592000")
	c.Data(http.StatusOK, contentType, data)
}

// AvatarRefresh 管理员触发刷新指定用户的 Gravatar 头像缓存。
//
// POST /api/v1/admin/users/:id/refresh-avatar
//
//	@Summary      刷新头像缓存
//	@Description  检查 Gravatar 是否有更新，若有则重新下载并更新缓存
//	@Tags         用户管理
//	@Produce      json
//	@Param        id  path  int  true  "用户 ID"
//	@Success      200  {object}  Response{data=object}  "刷新结果（updated 表示是否有更新）"
//	@Failure      400  {object}  Response  "无效的用户 ID"
//	@Failure      403  {object}  Response  "权限不足"
//	@Router       /admin/users/{id}/refresh-avatar [post]
func AvatarRefresh(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的用户 ID")
		return
	}

	updated, err := svc.Gravatar.RefreshAvatar(id)
	if err != nil {
		FailInternal(c, fmt.Sprintf("刷新头像失败: %v", err))
		return
	}

	Success(c, gin.H{"updated": updated})
}

// ============================================================
// 辅助函数
// ============================================================

// buildAvatarURL 根据用户 ID 生成内部头像 API 的 URL 路径。
func buildAvatarURL(userID uint64) string {
	return fmt.Sprintf("/api/v1/avatar/%d", userID)
}
