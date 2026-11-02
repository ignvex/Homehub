package handler

import (
	"encoding/json"
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
//
// **只保存，不抓取。** 除了 fetch_now 显式为 true，创建不会触发任何
// 网络请求——URL 不可达不该阻止你把它记下来。
type createRSSFeedRequest struct {
	URL string `json:"url" form:"url" binding:"required"`
	// Title 可留空：等第一次抓取补全，或之后自己改。抓取不会覆盖已填的值。
	Title  *string `json:"title" form:"title"`
	Remark *string `json:"remark" form:"remark"` // 使用者自己的备注
	// FetchInterval 省略时默认 3600 秒。
	FetchInterval *int `json:"fetch_interval" form:"fetch_interval"`
	// FetchBy 取 local / extension，省略时 local。
	FetchBy *string `json:"fetch_by" form:"fetch_by"`
	GroupID *uint64 `json:"group_id" form:"group_id"`
	// FetchNow 显式要求保存后抓一次（异步）。
	FetchNow bool `json:"fetch_now" form:"fetch_now"`
	// FetchEnabled 为 false 时保存但不参与调度。省略即为 true。
	FetchEnabled *bool `json:"fetch_enabled" form:"fetch_enabled"`
}

// optionalGroupID 区分 group_id 的三种 JSON 状态：字段缺失、显式 null、具体值。
//
// 不能直接用 *uint64：标准绑定对缺失字段和显式 null 都给出 nil，而两者语义相反
// —— 前者是“别动分组”，后者是“把分组清空”。分不开就永远清不掉分组。
type optionalGroupID struct {
	Set   bool
	Value *uint64
}

// UnmarshalJSON 只在字段真的出现时才被调用，因此 Set 的赋值就是“出现过”的证据。
func (o *optionalGroupID) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value uint64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

// updateRSSFeedRequest 更新 RSS 订阅源请求体。
type updateRSSFeedRequest struct {
	// 指针即三态：省略=不改，传值=写入，空串=清空（title / remark 适用）。
	Title         *string `json:"title"`
	Remark        *string `json:"remark"`
	FetchInterval *int    `json:"fetch_interval"`
	FetchBy       *string `json:"fetch_by"`
	// FetchEnabled 暂停/恢复这个源的调度。省略表示不改。
	FetchEnabled *bool `json:"fetch_enabled"`
	// group_id 传 null 表示清空分组，省略则保持原分组不变。
	GroupID optionalGroupID `json:"group_id" swaggertype:"integer" extensions:"x-nullable"`
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

	feed, err := svc.RSS.CreateFeed(service.CreateFeedInput{
		URL:           req.URL,
		Title:         req.Title,
		Remark:        req.Remark,
		FetchInterval: req.FetchInterval,
		FetchBy:       req.FetchBy,
		GroupID:       req.GroupID,
		FetchNow:      req.FetchNow,
		FetchEnabled:  req.FetchEnabled,
	})
	if err != nil {
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, feed)
}

// RSSUpdateFeed 更新订阅源。
//
//	@Summary		更新订阅源
//	@Description	修改订阅源的标题、抓取间隔或所属分组。group_id 为 null 时清空分组，省略字段则保持原分组不变。
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

	if err := svc.RSS.UpdateFeed(id, service.FeedUpdate{
		Title:         req.Title,
		Remark:        req.Remark,
		FetchInterval: req.FetchInterval,
		FetchBy:       req.FetchBy,
		FetchEnabled:  req.FetchEnabled,
		Group: service.GroupUpdate{
			Set:   req.GroupID.Set,
			Value: req.GroupID.Value,
		},
	}); err != nil {
		// 校验失败（间隔非正、fetch_by 不认识）是调用方能改的输入，
		// 与「订阅源不存在」分开报，否则用户会以为记录被删了。
		if errors.Is(err, service.ErrRSSFeedNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "订阅源不存在")
			return
		}
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, nil)
}

// batchUpdateRSSFeedsRequest 批量更新请求体。
//
// 复用 updateRSSFeedRequest 的字段定义而不是手工复制一份：
// 三态分组、空串清空、nil 跳过的语义必须与单条完全一致，
// 两份定义早晚会在某次改动后分叉。
type batchUpdateRSSFeedsRequest struct {
	IDs []uint64 `json:"ids" binding:"required"`
	updateRSSFeedRequest
}

// RSSBatchUpdateFeeds 对多个订阅源应用同一组变更。
//
//	@Summary		批量更新订阅源
//	@Description	一次事务内把同一组变更应用到多个订阅源，语义与单条更新一致；不存在的 ID 被忽略
//	@Tags			RSS 订阅
//	@Accept			json
//	@Produce		json
//	@Param			request	body		batchUpdateRSSFeedsRequest	true	"批量更新请求"
//	@Success		200		{object}	Response{data=object}	"实际更新的条数"
//	@Failure		400		{object}	Response	"未选中订阅源或参数无效"
//	@Router			/rss/feeds/batch [put]
func RSSBatchUpdateFeeds(c *gin.Context) {
	var req batchUpdateRSSFeedsRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的请求参数")
		return
	}

	affected, err := svc.RSS.UpdateFeeds(req.IDs, service.FeedUpdate{
		Title:         req.Title,
		Remark:        req.Remark,
		FetchInterval: req.FetchInterval,
		FetchBy:       req.FetchBy,
		FetchEnabled:  req.FetchEnabled,
		Group: service.GroupUpdate{
			Set:   req.GroupID.Set,
			Value: req.GroupID.Value,
		},
	})
	if err != nil {
		// 空选中与非法参数都是调用方能修正的输入，统一 400；
		// 只有数据库故障才该是 500，那种情况目前不会走到这里。
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, gin.H{"updated": affected})
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

// RSSSchedule 返回订阅源的调度总览。
//
//	@Summary		调度总览
//	@Description	每个源的下次抓取时间、是否到期、由谁抓取；本地抓取的源参与后台调度
//	@Tags			RSS 订阅
//	@Produce		json
//	@Success		200	{object}	Response{data=service.ScheduleState}
//	@Failure		500	{object}	Response	"服务器内部错误"
//	@Router			/rss/schedule [get]
func RSSSchedule(c *gin.Context) {
	state, err := svc.RSS.Schedule()
	if err != nil {
		FailInternal(c, err.Error())
		return
	}
	Success(c, state)
}
