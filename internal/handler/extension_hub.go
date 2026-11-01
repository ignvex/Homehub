package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/hubclient"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// Extension hub 通道 API
//
// 这一组端点把 HomeHub 作为宿主，接到 HomeHub Extension 的
// /api/v1/hub/* 签名通道上。它不代理上游的原始错误码：
// Extension 返回的 401 / 404 在 HomeHub 这侧一律折成 502，
// 否则前端会把「上游签名失败」误认成「自己掉登录了」并触发登出。
// ============================================================

// ExtensionHubStatus 查询对接状态。
//
//	@Summary		查询 Extension 对接状态
//	@Description	返回配置是否齐全、网络是否可达、签名是否通过、对方 RSS 模块是否启用
//	@Tags			Extension 对接
//	@Produce		json
//	@Success		200	{object}	Response{data=service.ExtensionHubStatus}
//	@Router			/extension/status [get]
func ExtensionHubStatus(c *gin.Context) {
	Success(c, svc.ExtensionHub.Status(c.Request.Context()))
}

// ExtensionHubListSubscriptions 列出 Extension 侧的订阅。
//
//	@Summary		列出 Extension 侧订阅
//	@Description	含抓取状态、连续失败次数与未读数，并标注本地是否也有同 URL 的订阅
//	@Tags			Extension 对接
//	@Produce		json
//	@Success		200	{object}	Response{data=[]service.ExtensionHubRemoteSubscription}
//	@Failure		502	{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/subscriptions [get]
func ExtensionHubListSubscriptions(c *gin.Context) {
	items, err := svc.ExtensionHub.RemoteSubscriptions(c.Request.Context())
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, items)
}

// pushExtensionSubscriptionRequest 推送单条订阅。
type pushExtensionSubscriptionRequest struct {
	FeedID uint64 `json:"feed_id" binding:"required"`
	// Enabled 省略表示不碰 Extension 侧的启停状态；显式 true 会把
	// 被自动停用的源恢复，并清零对方的连续失败计数。
	Enabled *bool `json:"enabled"`
	// FetchNow 省略时为 true，与协议默认一致。
	FetchNow *bool `json:"fetch_now"`
}

// ExtensionHubPushSubscription 把一条本地订阅推送到 Extension。
//
//	@Summary		推送单条订阅
//	@Description	按 URL 幂等：首次推送新建，再次推送更新。省略的字段在对方侧保持原值
//	@Tags			Extension 对接
//	@Accept			json
//	@Produce		json
//	@Param			body	body		pushExtensionSubscriptionRequest	true	"订阅源 ID 与可选的启停覆盖"
//	@Success		200		{object}	Response{data=service.ExtensionHubPushResult}
//	@Failure		404		{object}	Response	"本地订阅源不存在"
//	@Failure		502		{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/subscriptions [post]
func ExtensionHubPushSubscription(c *gin.Context) {
	var req pushExtensionSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请求参数错误："+err.Error())
		return
	}

	fetchNow := true
	if req.FetchNow != nil {
		fetchNow = *req.FetchNow
	}

	result, err := svc.ExtensionHub.PushFeed(c.Request.Context(), req.FeedID, req.Enabled, fetchNow)
	if err != nil {
		if errors.Is(err, service.ErrRSSFeedNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, err.Error())
			return
		}
		failExtensionHub(c, err)
		return
	}
	Success(c, result)
}

// ExtensionHubSync 把本地全部订阅补推到 Extension。
//
//	@Summary		全量对账
//	@Description	只推送不删除：删除会连带清掉对方已抓取的条目，必须逐条确认
//	@Tags			Extension 对接
//	@Produce		json
//	@Success		200	{object}	Response{data=service.ExtensionHubSyncResult}
//	@Failure		502	{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/subscriptions/sync [post]
func ExtensionHubSync(c *gin.Context) {
	result, err := svc.ExtensionHub.SyncAll(c.Request.Context())
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, result)
}

// ExtensionHubDeleteSubscription 删除 Extension 侧的订阅。
//
//	@Summary		删除 Extension 侧订阅
//	@Description	同时清理对方已抓取的条目与 blob 引用，不可恢复
//	@Tags			Extension 对接
//	@Produce		json
//	@Param			id	path		int	true	"Extension 侧订阅 ID"
//	@Success		200	{object}	Response	"已删除或本就不存在"
//	@Failure		502	{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/subscriptions/{id} [delete]
func ExtensionHubDeleteSubscription(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		FailBadRequest(c, "订阅 ID 不合法")
		return
	}

	alreadyDeleted, err := svc.ExtensionHub.DeleteRemoteSubscription(c.Request.Context(), id)
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, gin.H{"deleted": true, "already_deleted": alreadyDeleted})
}

// ExtensionHubListKeys 列出已签发的访问密钥。
//
//	@Summary		列出访问密钥
//	@Description	响应不含明文也不含哈希；live 由服务端计算，宿主不要自己比较过期时间
//	@Tags			Extension 对接
//	@Produce		json
//	@Success		200	{object}	Response{data=[]hubclient.AccessKey}
//	@Failure		502	{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/keys [get]
func ExtensionHubListKeys(c *gin.Context) {
	keys, err := svc.ExtensionHub.ListKeys(c.Request.Context())
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, keys)
}

// issueExtensionKeyRequest 签发访问密钥。
type issueExtensionKeyRequest struct {
	Name string `json:"name" binding:"required"`
	// ExpiresIn 是 Go 风格的时长串（如 "720h"）。省略表示永不过期。
	ExpiresIn *string `json:"expires_in"`
}

// ExtensionHubIssueKey 签发一把访问密钥。
//
//	@Summary		签发访问密钥
//	@Description	明文 key 只在本响应出现一次，之后任何接口都拿不回
//	@Tags			Extension 对接
//	@Accept			json
//	@Produce		json
//	@Param			body	body		issueExtensionKeyRequest	true	"名称与有效期"
//	@Success		200		{object}	Response{data=hubclient.CreatedKey}
//	@Failure		502		{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/keys [post]
func ExtensionHubIssueKey(c *gin.Context) {
	var req issueExtensionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请求参数错误："+err.Error())
		return
	}

	key, err := svc.ExtensionHub.IssueKey(c.Request.Context(), req.Name, req.ExpiresIn)
	if err != nil {
		if errors.Is(err, service.ErrExtensionHubInvalidInput) || isBadKeyRequest(err) {
			FailBadRequest(c, err.Error())
			return
		}
		failExtensionHub(c, err)
		return
	}
	Success(c, key)
}

// ExtensionHubRevokeKey 撤销一把访问密钥。
//
//	@Summary		撤销访问密钥
//	@Description	同时撤销它换出去的会话；该密钥名下的数据仍可被同一主人的其他凭证读取
//	@Tags			Extension 对接
//	@Produce		json
//	@Param			id	path		string	true	"密钥 ID"
//	@Success		200	{object}	Response	"已撤销或本就不存在"
//	@Failure		502	{object}	Response	"上游 Extension 不可达或返回错误"
//	@Router			/extension/keys/{id} [delete]
func ExtensionHubRevokeKey(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		FailBadRequest(c, "密钥 ID 不能为空")
		return
	}

	alreadyDeleted, err := svc.ExtensionHub.RevokeKey(c.Request.Context(), id)
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, gin.H{"revoked": true, "already_deleted": alreadyDeleted})
}

// isBadKeyRequest 区分「调用方给的参数不对」与「上游出错」。
//
// 名称空、有效期格式错这类问题由 Extension 以 400 invalid_request 拒绝。
// 回 400 比 502 更准确：这是调用方能自己改的输入，不是上游故障。
func isBadKeyRequest(err error) bool {
	var apiErr *hubclient.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest
}

// responseErrorCode 把 service 层的错误折成 HomeHub 的错误码与 HTTP 状态。
//
// Extension 返回的状态码不透传：401 会让前端误以为自己的会话失效，
// 404 会让前端以为 HomeHub 里没有这个资源。两者都必须是 502。
//
// 本地存储故障同样不能归入上游：那条提示会把人送去查另一台机器。
func responseErrorCode(err error) (httpStatus, code int, message string) {
	switch {
	case errors.Is(err, service.ErrExtensionHubDisabled):
		return http.StatusServiceUnavailable, CodeExtensionDisabled, err.Error()
	case errors.Is(err, service.ErrExtensionHubStorage):
		return http.StatusInternalServerError, CodeInternalError, err.Error()
	default:
		return http.StatusBadGateway, CodeExtensionUpstream, err.Error()
	}
}

// failExtensionHub 统一输出 Extension 对接错误。
func failExtensionHub(c *gin.Context, err error) {
	httpStatus, code, message := responseErrorCode(err)
	Fail(c, httpStatus, code, message)
}

// ============================================================
// 签名私钥
//
// 三个接口都不返回私钥本身：GET 只给公钥、指纹与来源。
// 这是与「签发访问密钥」一致的原则——凭据可以写进去，但读不出来。
// ============================================================

// ExtensionHubGetKey 查询当前签名私钥的来源与公钥。
//
//	@Summary		查询签名私钥来源
//	@Description	返回密钥来自数据库还是文件，以及公钥与指纹；永不返回私钥
//	@Tags			Extension 对接
//	@Produce		json
//	@Success		200	{object}	Response{data=service.ExtensionHubKeyInfo}
//	@Router			/extension/key [get]
func ExtensionHubGetKey(c *gin.Context) {
	info, err := svc.ExtensionHub.KeyInfo()
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, info)
}

// setExtensionKeyRequest 上传私钥。
type setExtensionKeyRequest struct {
	// PrivateKey 接受 PKCS#8 PEM、裸 32 字节种子的 base64、或 openssl 的原文。
	PrivateKey string `json:"private_key" binding:"required"`
}

// ExtensionHubSetKey 把签名私钥存入数据库。
//
//	@Summary		保存签名私钥到数据库
//	@Description	接受 PKCS#8 PEM 或 base64；入库前自签自验。多实例共用同一个数据库时只需配置一次
//	@Tags			Extension 对接
//	@Accept			json
//	@Produce		json
//	@Param			body	body		setExtensionKeyRequest	true	"私钥内容"
//	@Success		200		{object}	Response{data=service.ExtensionHubKeyInfo}
//	@Failure		400		{object}	Response	"私钥无法解析或自检失败"
//	@Router			/extension/key [put]
func ExtensionHubSetKey(c *gin.Context) {
	var req setExtensionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请求参数错误："+err.Error())
		return
	}

	info, err := svc.ExtensionHub.SetKey(req.PrivateKey)
	if err != nil {
		if errors.Is(err, service.ErrExtensionHubInvalidInput) {
			FailBadRequest(c, err.Error())
			return
		}
		failExtensionHub(c, err)
		return
	}
	Success(c, info)
}

// ExtensionHubDeleteKey 移除数据库里的私钥，来源退回 private_key_path。
//
//	@Summary		移除数据库里的签名私钥
//	@Description	删除后若配置了 private_key_path 会回退到文件；两处都没有则无法签名
//	@Tags			Extension 对接
//	@Produce		json
//	@Success		200	{object}	Response
//	@Router			/extension/key [delete]
func ExtensionHubDeleteKey(c *gin.Context) {
	removed, err := svc.ExtensionHub.DeleteKey()
	if err != nil {
		failExtensionHub(c, err)
		return
	}
	Success(c, gin.H{"deleted": removed})
}
