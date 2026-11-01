package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
)

// 头像响应的缓存策略。
const (
	// /avatar/{id} 的 URL 唯一标识一张图，可以缓存，但必须每次用 ETag 校验：
	// 旧实现给了 30 天 public，管理员刷新缓存后浏览器仍显示旧图。
	avatarCacheControl = "private, no-cache"
	// /avatar/me 的 URL 不区分用户，任何缓存都会在切换账号时串号。
	// 前端已改为优先使用 /avatar/{id}，这里只作兜底。
	avatarNoStore = "no-store"
)

// ============================================================
// 头像 API
// ============================================================

// AvatarGet 返回指定用户的头像图片。
//
// GET /api/v1/avatar/:id
//
// 从数据库缓存读取头像；缓存不存在时自动从上游（官方 Gravatar 或
// extension）下载并缓存。响应带 ETag，重复请求可用 If-None-Match 换 304。
// 尺寸由服务端 default_size 统一决定，所有显示位置共用同一张图。
//
//	@Summary      获取用户头像
//	@Description  返回缓存的头像图片，尺寸由配置 gravatar.default_size 决定
//	@Tags         头像
//	@Produce      image/*
//	@Param        id  path  int  true  "用户 ID"
//	@Success      200  {file}  binary  "头像图片"
//	@Failure      304  "头像未变化（If-None-Match 命中）"
//	@Failure      400  {object}  Response  "无效的用户 ID"
//	@Failure      404  {object}  Response  "用户不存在、未设置邮箱或上游无头像"
//	@Failure      502  {object}  Response  "头像上游不可用"
//	@Router       /avatar/{id} [get]
func AvatarGet(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "无效的用户 ID")
		return
	}
	serveAvatar(c, id, avatarCacheControl)
}

// AvatarGetMe 返回当前登录用户的头像图片。
//
// GET /api/v1/avatar/me
//
// 该 URL 对所有用户相同，因此响应禁止任何缓存（包括私有缓存），
// 否则同一浏览器切换账号后会继续显示上一个用户的头像。
//
//	@Summary      获取当前用户头像
//	@Description  返回当前登录用户的缓存头像
//	@Tags         头像
//	@Produce      image/*
//	@Success      200  {file}  binary  "头像图片"
//	@Failure      401  {object}  Response  "未登录"
//	@Failure      404  {object}  Response  "未设置邮箱或上游无头像"
//	@Failure      502  {object}  Response  "头像上游不可用"
//	@Router       /avatar/me [get]
func AvatarGetMe(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists {
		FailUnauthorized(c, "未登录")
		return
	}
	user, ok := u.(*model.User)
	if !ok {
		FailUnauthorized(c, "未登录")
		return
	}
	serveAvatar(c, user.ID, avatarNoStore)
}

// AvatarRefresh 管理员触发刷新指定用户的头像缓存。
//
// POST /api/v1/admin/users/:id/refresh-avatar
//
// 以 ETag/Last-Modified 发条件请求：上游确认未变化则保留本地缓存，
// 有变化则重新下载；上游返回 404 时清除缓存。
//
//	@Summary      刷新头像缓存
//	@Description  检查上游是否有更新，有变化则重新下载，上游确认无头像则清除缓存
//	@Tags         用户管理
//	@Produce      json
//	@Param        id  path  int  true  "用户 ID"
//	@Success      200  {object}  Response{data=object}  "刷新结果（updated 表示缓存是否变化）"
//	@Failure      400  {object}  Response  "无效的用户 ID 或用户未设置邮箱"
//	@Failure      403  {object}  Response  "权限不足或 Gravatar 已关闭"
//	@Failure      404  {object}  Response  "用户不存在"
//	@Failure      502  {object}  Response  "头像上游不可用"
//	@Router       /admin/users/{id}/refresh-avatar [post]
func AvatarRefresh(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的用户 ID")
		return
	}

	updated, err := svc.Gravatar.RefreshAvatar(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGravatarDisabled):
			Fail(c, http.StatusForbidden, CodeForbidden, "Gravatar 头像功能已在配置中关闭")
		case errors.Is(err, service.ErrUserNotFound):
			Fail(c, http.StatusNotFound, CodeUserNotFound, "用户不存在")
		case errors.Is(err, service.ErrAvatarUnavailable):
			FailBadRequest(c, "用户未设置邮箱")
		case errors.Is(err, service.ErrAvatarEndpointHTML):
			Fail(c, http.StatusBadGateway, CodeInternalError,
				"头像端点返回了 HTML 页面：extension_url 可能指向了没有 /avatar 的服务，请检查配置")
		case errors.Is(err, service.ErrAvatarUpstream), errors.Is(err, service.ErrAvatarInvalid):
			Fail(c, http.StatusBadGateway, CodeInternalError, "头像上游不可用")
		default:
			// 不把内部错误原文返回给客户端，避免泄露上游地址与数据库细节。
			FailInternal(c, "刷新头像失败")
		}
		return
	}

	Success(c, gin.H{"updated": updated})
}

// ============================================================
// 辅助函数
// ============================================================

// serveAvatar 读取并输出头像，统一处理条件请求与错误映射。
func serveAvatar(c *gin.Context, userID uint64, cacheControl string) {
	avatar, err := svc.Gravatar.GetAvatarData(c.Request.Context(), userID)
	if err != nil {
		writeAvatarError(c, err)
		return
	}

	c.Header("ETag", avatar.ETag)
	// 304 也带缓存指令：RFC 7232 允许且推荐，浏览器据此刷新缓存有效期。
	c.Header("Cache-Control", cacheControl)

	// If-None-Match 可能包含多个实体标签（逗号分隔），逐项比对。
	if etagMatches(c.GetHeader("If-None-Match"), avatar.ETag) {
		c.Status(http.StatusNotModified)
		return
	}

	c.Data(http.StatusOK, avatar.ContentType, avatar.Data)
}

// writeAvatarError 把服务层错误映射成 HTTP 状态码。
//
// 用户侧原因（关闭开关、用户不存在、无邮箱、上游确认无图）一律 404，
// 让前端 UserAvatar 的 onError 降级为首字母占位；
// 上游故障单独用 502，便于运维区分「没有头像」和「头像服务挂了」。
func writeAvatarError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrGravatarDisabled),
		errors.Is(err, service.ErrUserNotFound),
		errors.Is(err, service.ErrAvatarUnavailable),
		errors.Is(err, service.ErrAvatarNotFound):
		c.String(http.StatusNotFound, "头像不可用")
	case errors.Is(err, service.ErrAvatarEndpointHTML):
		c.String(http.StatusBadGateway, "头像端点返回 HTML，请检查 extension_url")
	case errors.Is(err, service.ErrAvatarUpstream),
		errors.Is(err, service.ErrAvatarInvalid):
		c.String(http.StatusBadGateway, "头像上游不可用")
	default:
		c.String(http.StatusInternalServerError, "头像服务内部错误")
	}
}

// etagMatches 判断请求头中的 If-None-Match 是否命中当前 ETag。
//
// 兼容 W/ 弱校验前缀与 * 通配，避免因为代理改写头导致条件请求永远失效。
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" || etag == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

// buildAvatarURL 根据用户 ID 生成内部头像 API 的 URL 路径。
func buildAvatarURL(userID uint64) string {
	return "/api/v1/avatar/" + strconv.FormatUint(userID, 10)
}
