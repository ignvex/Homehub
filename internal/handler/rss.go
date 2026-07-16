package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
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

	if err := svc.RSS.FetchFeed(id); err != nil {
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
	if err := svc.RSS.FetchAll(); err != nil {
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

	entries, total, err := svc.RSS.ListEntries(feedID, groupID, page, perPage)
	if err != nil {
		FailInternal(c, "查询条目失败")
		return
	}
	Success(c, gin.H{"list": entries, "total": total, "page": page, "per_page": perPage})
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
