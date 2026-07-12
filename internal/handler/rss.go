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
	URL string `json:"url" form:"url" binding:"required"`
}

// updateRSSFeedRequest 更新 RSS 订阅源请求体。
type updateRSSFeedRequest struct {
	Title         *string `json:"title"`
	FetchInterval *int    `json:"fetch_interval"`
}

// RSSListFeeds 列表所有订阅源。
//
// GET /api/v1/rss/feeds
func RSSListFeeds(c *gin.Context) {
	feeds, err := svc.RSS.ListFeeds()
	if err != nil {
		FailInternal(c, "查询订阅源失败")
		return
	}
	Success(c, feeds)
}

// RSSGetFeed 获取单个订阅源详情。
//
// GET /api/v1/rss/feeds/:id
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
// POST /api/v1/rss/feeds
func RSSCreateFeed(c *gin.Context) {
	var req createRSSFeedRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供订阅源 URL")
		return
	}

	feed, err := svc.RSS.CreateFeed(req.URL)
	if err != nil {
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, feed)
}

// RSSUpdateFeed 更新订阅源。
//
// PUT /api/v1/rss/feeds/:id
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

	if err := svc.RSS.UpdateFeed(id, title, interval); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "订阅源不存在")
		return
	}
	Success(c, nil)
}

// RSSDeleteFeed 删除订阅源及其所有条目。
//
// DELETE /api/v1/rss/feeds/:id
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
// POST /api/v1/rss/feeds/:id/fetch
func RSSFetchFeed(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的订阅源 ID")
		return
	}

	if err := svc.RSS.FetchFeed(id); err != nil {
		FailInternal(c, "抓取失败: "+err.Error())
		return
	}
	Success(c, nil)
}

// RSSFetchAll 手动触发全部到期订阅源抓取。
//
// POST /api/v1/rss/fetch-all
func RSSFetchAll(c *gin.Context) {
	if err := svc.RSS.FetchAll(); err != nil {
		FailInternal(c, "抓取失败: "+err.Error())
		return
	}
	Success(c, nil)
}

// RSSListEntries 获取指定订阅源的条目列表。
//
// GET /api/v1/rss/entries?feed_id=&page=&per_page=
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

	page := queryInt(c, "page", 1)
	perPage := queryInt(c, "per_page", 20)

	entries, total, err := svc.RSS.ListEntries(feedID, page, perPage)
	if err != nil {
		FailInternal(c, "查询条目失败")
		return
	}
	Success(c, gin.H{"list": entries, "total": total, "page": page, "per_page": perPage})
}
