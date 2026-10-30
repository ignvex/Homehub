package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// RSS 订阅管理 API
// ============================================================

// createRSSFeedRequest 添加 RSS 订阅源请求体。
type createRSSFeedRequest struct {
	URL     string  `json:"url" form:"url" binding:"required"`
	GroupID *uint64 `json:"group_id" form:"group_id"`
}

// updateRSSFeedRequest 更新 RSS 订阅源请求体。
type updateRSSFeedRequest struct {
	Title         *string `json:"title"`
	FetchInterval *int    `json:"fetch_interval"`
	GroupID       *uint64 `json:"group_id"`
}

// RSSListFeeds 列表所有订阅源。
//
//	@Summary		获取订阅源列表
//	@Description	返回所有 RSS 订阅源及其最新状态，可按分组筛选
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			group_id	query		int		false	"分组 ID 筛选"
//	@Success		200			{object}	Response	"订阅源列表"
//	@Failure		500			{object}	Response	"服务器内部错误"
//	@Router			/rss/feeds [get]
func RSSListFeeds(c *gin.Context) {
	var groupID *uint64
	if gid := c.Query("group_id"); gid != "" {
		id, err := strconv.ParseUint(gid, 10, 64)
		if err == nil {
			groupID = &id
		}
	}

	feeds, err := svc.RSS.ListFeeds(groupID)
	if err != nil {
		FailInternal(c, "查询订阅源失败")
		return
	}
	Success(c, feeds)
}

// RSSGetFeed 获取单个订阅源详情。
//
//	@Summary		获取订阅源详情
//	@Description	根据 ID 获取单个 RSS 订阅源的详细信息
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			id	path		int		true	"订阅源 ID"
//	@Success		200	{object}	Response	"订阅源详情"
//	@Failure		400	{object}	Response	"无效的订阅源 ID"
//	@Failure		404	{object}	Response	"订阅源不存在"
//	@Router			/rss/feeds/{id} [get]
func RSSGetFeed(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的订阅源 ID")
		return
	}

	feed, err := svc.RSS.GetFeed(id)
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "订阅源不存在")
		return
	}
	Success(c, feed)
}

// RSSCreateFeed 添加订阅源。
//
//	@Summary		添加订阅源
//	@Description	添加新的 RSS 订阅源，自动抓取标题和条目
//	@Tags			RSS 订阅
//	@Accept			json
//	@Produce		json
//	@Param			request	body		createRSSFeedRequest	true	"订阅源 URL"
//	@Success		200		{object}	Response				"新创建的订阅源"
//	@Failure		400		{object}	Response				"无效的 URL"
//	@Router			/rss/feeds [post]
func RSSCreateFeed(c *gin.Context) {
	var req createRSSFeedRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供订阅源 URL")
		return
	}

	feed, err := svc.RSS.CreateFeed(req.URL, req.GroupID)
	if err != nil {
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, feed)
}

// RSSUpdateFeed 更新订阅源。
//
//	@Summary		更新订阅源
//	@Description	修改订阅源的标题、抓取间隔或所属分组
//	@Tags			RSS 订阅
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int						true	"订阅源 ID"
//	@Param			request	body		updateRSSFeedRequest	true	"更新请求"
//	@Success		200		{object}	Response				"更新成功"
//	@Failure		400		{object}	Response				"无效的请求参数"
//	@Failure		404		{object}	Response				"订阅源不存在"
//	@Router			/rss/feeds/{id} [put]
func RSSUpdateFeed(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的订阅源 ID")
		return
	}

	var req updateRSSFeedRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的请求参数")
		return
	}

	title := ""
	if req.Title != nil {
		title = *req.Title
	}
	interval := 0
	if req.FetchInterval != nil {
		interval = *req.FetchInterval
	}

	if err := svc.RSS.UpdateFeed(id, title, interval, req.GroupID); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "订阅源不存在")
		return
	}
	Success(c, nil)
}

// RSSDeleteFeed 删除订阅源及其所有条目。
//
//	@Summary		删除订阅源
//	@Description	删除订阅源及其所有关联条目
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			id	path		int		true	"订阅源 ID"
//	@Success		200	{object}	Response	"删除成功"
//	@Failure		400	{object}	Response	"无效的订阅源 ID"
//	@Failure		404	{object}	Response	"订阅源不存在"
//	@Router			/rss/feeds/{id} [delete]
func RSSDeleteFeed(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的订阅源 ID")
		return
	}

	if err := svc.RSS.DeleteFeed(id); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "订阅源不存在")
		return
	}
	Success(c, nil)
}

// RSSFetchFeed 手动触发订阅源抓取。
//
//	@Summary		手动抓取订阅源
//	@Description	立即触发指定订阅源的抓取更新
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			id	path		int		true	"订阅源 ID"
//	@Success		200	{object}	Response	"抓取成功"
//	@Failure		400	{object}	Response	"无效的订阅源 ID"
//	@Failure		500	{object}	Response	"抓取失败"
//	@Router			/rss/feeds/{id}/fetch [post]
func RSSFetchFeed(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的订阅源 ID")
		return
	}

	// 用请求 ctx：客户端断开时中止抓取，避免无谓占用并发槽。
	if err := svc.RSS.FetchFeed(c.Request.Context(), id); err != nil {
		FailInternal(c, "抓取失败")
		return
	}
	Success(c, nil)
}

// RSSFetchAll 手动触发全部到期订阅源抓取。
//
//	@Summary		抓取全部订阅源
//	@Description	立即触发所有到期订阅源的批量抓取
//	@Tags			RSS 订阅
//	@Produce		json
//	@Success		200	{object}	Response	"抓取成功"
//	@Failure		500	{object}	Response	"抓取失败"
//	@Router			/rss/fetch-all [post]
func RSSFetchAll(c *gin.Context) {
	if err := svc.RSS.FetchAll(c.Request.Context()); err != nil {
		FailInternal(c, "抓取失败")
		return
	}
	Success(c, nil)
}

// RSSListEntries 获取指定订阅源的条目列表。
//
//	@Summary		获取 RSS 条目列表
//	@Description	分页获取 RSS 订阅条目，可按订阅源筛选
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			feed_id		query		int		false	"订阅源 ID 筛选"
//	@Param			page		query		int		false	"页码（默认 1）"
//	@Param			per_page	query		int		false	"每页数量（默认 20）"
//	@Success		200			{object}	Response	"条目列表（含分页信息）"
//	@Failure		400			{object}	Response	"无效的 feed_id"
//	@Failure		500			{object}	Response	"服务器内部错误"
//	@Router			/rss/entries [get]
func RSSListEntries(c *gin.Context) {
	feedID := uint64(0)
	if fid := c.Query("feed_id"); fid != "" {
		var err error
		feedID, err = strconv.ParseUint(fid, 10, 64)
		if err != nil {
			FailBadRequest(c, "无效的 feed_id")
			return
		}
	}

	groupID := uint64(0)
	if gid := c.Query("group_id"); gid != "" {
		var err error
		groupID, err = strconv.ParseUint(gid, 10, 64)
		if err != nil {
			FailBadRequest(c, "无效的 group_id")
			return
		}
	}

	page := queryInt(c, "page", 1)
	perPage := queryInt(c, "per_page", 20)
	// magnet 维度筛选：has / locked / unlocked / pikpak_saved / pikpak_failed。
	magnet := c.Query("magnet")

	entries, total, err := svc.RSS.ListEntries(service.EntryFilter{
		FeedID: feedID, GroupID: groupID,
		Page: page, PerPage: perPage,
		Magnet: magnet,
	})
	if err != nil {
		// 非法筛选条件是客户端问题，不该报 500。
		if strings.Contains(err.Error(), "无效的磁力筛选条件") {
			FailBadRequest(c, err.Error())
			return
		}
		FailInternal(c, "查询条目失败")
		return
	}
	Success(c, gin.H{"list": entries, "total": total, "page": page, "per_page": perPage})
}

// ============================================================
// 磁力链接锁定与 PikPak
// ============================================================

// RSSLockMagnet 锁定条目的磁力链接，使其不被后续抓取覆盖。
//
//	@Summary		锁定磁力链接
//	@Tags			磁力链接
//	@Produce		json
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response	"锁定成功"
//	@Router			/rss/entries/{id}/magnet/lock [put]
func RSSLockMagnet(c *gin.Context) {
	entryID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的条目 ID")
		return
	}

	operator := getCurrentUser(c)
	if operator == nil {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "未登录")
		return
	}

	if err := svc.RSS.LockMagnet(entryID, operator); err != nil {
		if errors.Is(err, service.ErrRSSEntryNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "条目不存在")
			return
		}
		FailInternal(c, "锁定磁力链接失败")
		return
	}
	Success(c, nil)
}

// RSSUnlockMagnet 解除磁力链接锁定。
//
//	@Summary		解除磁力锁定
//	@Tags			磁力链接
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response	"解锁成功"
//	@Router			/rss/entries/{id}/magnet/lock [delete]
func RSSUnlockMagnet(c *gin.Context) {
	entryID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的条目 ID")
		return
	}

	if err := svc.RSS.UnlockMagnet(entryID); err != nil {
		if errors.Is(err, service.ErrRSSEntryNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "条目不存在")
			return
		}
		FailInternal(c, "解除磁力锁定失败")
		return
	}
	Success(c, nil)
}

// RSSSubmitMagnet 把条目的磁力链接提交给 PikPak 离线下载。
//
//	@Summary		提交磁力链接到 PikPak
//	@Description	要求条目已有磁力链接且已锁定；PikPak 未启用时返回 503
//	@Tags			磁力链接
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response{data=service.RSSEntryView}	"提交成功"
//	@Router			/rss/entries/{id}/magnet/pikpak [post]
func RSSSubmitMagnet(c *gin.Context) {
	entryID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的条目 ID")
		return
	}

	entry, err := svc.RSS.SubmitMagnetToPikPak(c.Request.Context(), entryID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPikPakDisabled):
			Fail(c, http.StatusServiceUnavailable, CodeInternalError, "PikPak 集成未启用")
		case errors.Is(err, service.ErrRSSEntryNotFound):
			Fail(c, http.StatusNotFound, CodeNotFound, "条目不存在")
		case errors.Is(err, service.ErrNoMagnet):
			FailBadRequest(c, "该条目没有磁力链接")
		case errors.Is(err, service.ErrMagnetNotLocked):
			FailBadRequest(c, "请先锁定磁力链接再提交")
		default:
			// 提交失败已经把原因写进 pikpak_status / pikpak_error，
			// 这里返回 502 让调用方知道是上游问题而不是本地参数错误。
			Fail(c, http.StatusBadGateway, CodeInternalError, err.Error())
		}
		return
	}
	Success(c, entry)
}

// ============================================================
// RSS 分组管理 API
// ============================================================

// createRSSGroupRequest 创建 RSS 分组请求体。
type createRSSGroupRequest struct {
	Name string `json:"name" form:"name" binding:"required"`
}

// updateRSSGroupRequest 更新 RSS 分组请求体。
type updateRSSGroupRequest struct {
	Name      *string `json:"name"`
	SortOrder *int    `json:"sort_order"`
}

// RSSListGroups 获取所有 RSS 分组。
//
//	@Summary		获取分组列表
//	@Description	返回所有 RSS 订阅分组，按排序序号排列
//	@Tags			RSS 订阅
//	@Produce		json
//	@Success		200	{object}	Response	"分组列表"
//	@Failure		500	{object}	Response	"服务器内部错误"
//	@Router			/rss/groups [get]
func RSSListGroups(c *gin.Context) {
	groups, err := svc.RSS.ListGroups()
	if err != nil {
		FailInternal(c, "查询分组列表失败")
		return
	}
	Success(c, groups)
}

// RSSGetGroup 获取单个 RSS 分组详情（含关联订阅源）。
//
//	@Summary		获取分组详情
//	@Description	返回分组信息及其下所有订阅源
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			id	path		int		true	"分组 ID"
//	@Success		200	{object}	Response	"分组详情（含订阅源列表）"
//	@Failure		400	{object}	Response	"无效的分组 ID"
//	@Failure		404	{object}	Response	"分组不存在"
//	@Router			/rss/groups/{id} [get]
func RSSGetGroup(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的分组 ID")
		return
	}

	group, err := svc.RSS.GetGroup(id)
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "分组不存在")
		return
	}
	Success(c, group)
}

// RSSCreateGroup 创建 RSS 分组。
//
//	@Summary		创建分组
//	@Description	创建新的 RSS 订阅分组
//	@Tags			RSS 订阅
//	@Accept			json
//	@Produce		json
//	@Param			request	body		createRSSGroupRequest	true	"分组名称"
//	@Success		200		{object}	Response				"新创建的分组"
//	@Failure		400		{object}	Response				"分组名称不能为空"
//	@Router			/rss/groups [post]
func RSSCreateGroup(c *gin.Context) {
	var req createRSSGroupRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供分组名称")
		return
	}

	group, err := svc.RSS.CreateGroup(req.Name)
	if err != nil {
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, group)
}

// RSSUpdateGroup 更新 RSS 分组。
//
//	@Summary		更新分组
//	@Description	修改分组的名称和排序序号
//	@Tags			RSS 订阅
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int						true	"分组 ID"
//	@Param			request	body		updateRSSGroupRequest	true	"更新请求"
//	@Success		200		{object}	Response				"更新成功"
//	@Failure		400		{object}	Response				"无效的请求参数"
//	@Failure		404		{object}	Response				"分组不存在"
//	@Router			/rss/groups/{id} [put]
func RSSUpdateGroup(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的分组 ID")
		return
	}

	var req updateRSSGroupRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的请求参数")
		return
	}

	name := ""
	if req.Name != nil {
		name = *req.Name
	}

	if err := svc.RSS.UpdateGroup(id, name, req.SortOrder); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "分组不存在")
		return
	}
	Success(c, nil)
}

// RSSDeleteGroup 删除 RSS 分组。
//
//	@Summary		删除分组
//	@Description	删除分组（其下订阅源将移回未分组）
//	@Tags			RSS 订阅
//	@Produce		json
//	@Param			id	path		int		true	"分组 ID"
//	@Success		200	{object}	Response	"删除成功"
//	@Failure		400	{object}	Response	"无效的分组 ID"
//	@Failure		404	{object}	Response	"分组不存在"
//	@Router			/rss/groups/{id} [delete]
func RSSDeleteGroup(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的分组 ID")
		return
	}

	if err := svc.RSS.DeleteGroup(id); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "分组不存在")
		return
	}
	Success(c, nil)
}

// RSSPikPakStatus 返回 PikPak 集成的当前状态。
//
// 只暴露“是否启用”和“是否配好凭据”，不返回用户名密码。
// 前端据此决定是否显示“提交到 PikPak”按钮。
//
//	@Summary		PikPak 集成状态
//	@Tags			磁力链接
//	@Produce		json
//	@Success		200	{object}	Response	"状态"
//	@Router			/rss/pikpak [get]
func RSSPikPakStatus(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		Fail(c, http.StatusServiceUnavailable, CodeInternalError, "配置尚未加载")
		return
	}

	p := cfg.RSS.PikPak
	// “可用”= 启用且凭据齐全。只启用不填凭据是最常见的配置错误，
	// 直接告诉调用方还缺什么，比让它点提交再收 500 好。
	ready := p.Enabled && p.Username != "" && p.Password != "" &&
		p.ClientID != "" && p.ClientSecret != ""

	Success(c, gin.H{
		"enabled":   p.Enabled,
		"ready":     ready,
		"auto_lock": p.AutoLock,
		// 显示实际生效值，而不是配置里的原始空串 ——
		// 否则“没填”和“填了但没生效”看起来一样。
		"endpoint":     service.EffectivePikPakEndpoint(p),
		"timeout_secs": int(service.EffectivePikPakTimeout(p).Seconds()),
		// 凭据只报“填没填”，不回传值。
		"has_credentials": p.Username != "" && p.Password != "",
		"has_client":      p.ClientID != "" && p.ClientSecret != "",
	})
}
