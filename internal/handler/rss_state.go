package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// RSS 阅读状态（按用户）
// ============================================================
//
// 已读与星标是个人数据，不是内容写：登录即可（含 5 级只读用户）。

// markEntriesReadRequest 批量标记已读请求体。各条件之间是 AND；全部省略即全部已读。
type markEntriesReadRequest struct {
	FeedID  *uint64 `json:"feed_id"`
	GroupID *uint64 `json:"group_id"`
	// EntryIDs 省略表示不按 id 限定；显式空数组会被拒绝。
	EntryIDs []uint64 `json:"entry_ids"`
	// Before 只标记发布时间不晚于它的条目，RFC3339。
	Before *time.Time `json:"before" swaggertype:"string" format:"date-time"`
}

// rssSetEntryState 是四个单条端点的公共实现。
func rssSetEntryState(c *gin.Context, set func(userID, entryID uint64) error) {
	entryID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的条目 ID")
		return
	}
	userID := getCurrentUserID(c)
	if userID == 0 {
		FailUnauthorized(c, "未登录")
		return
	}
	if err := set(userID, entryID); err != nil {
		if errors.Is(err, service.ErrRSSEntryNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "条目不存在")
			return
		}
		FailInternal(c, "更新阅读状态失败")
		return
	}
	Success(c, nil)
}

// RSSMarkEntryRead 把条目标记为已读。
//
//	@Summary		标记已读
//	@Description	对当前用户把条目标记为已读，幂等
//	@Tags			RSS 阅读状态
//	@Produce		json
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response	"成功"
//	@Failure		404	{object}	Response	"条目不存在"
//	@Router			/rss/entries/{id}/read [put]
func RSSMarkEntryRead(c *gin.Context) {
	rssSetEntryState(c, func(u, e uint64) error { return svc.RSS.SetEntryRead(u, e, true) })
}

// RSSMarkEntryUnread 把条目标记为未读。
//
//	@Summary		标记未读
//	@Description	对当前用户把条目恢复为未读，幂等
//	@Tags			RSS 阅读状态
//	@Produce		json
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response	"成功"
//	@Failure		404	{object}	Response	"条目不存在"
//	@Router			/rss/entries/{id}/read [delete]
func RSSMarkEntryUnread(c *gin.Context) {
	rssSetEntryState(c, func(u, e uint64) error { return svc.RSS.SetEntryRead(u, e, false) })
}

// RSSStarEntry 给条目加星标。
//
//	@Summary		加星标
//	@Description	对当前用户给条目加星标，幂等
//	@Tags			RSS 阅读状态
//	@Produce		json
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response	"成功"
//	@Failure		404	{object}	Response	"条目不存在"
//	@Router			/rss/entries/{id}/star [put]
func RSSStarEntry(c *gin.Context) {
	rssSetEntryState(c, func(u, e uint64) error { return svc.RSS.SetEntryStarred(u, e, true) })
}

// RSSUnstarEntry 取消星标。
//
//	@Summary		取消星标
//	@Description	对当前用户取消条目星标，幂等
//	@Tags			RSS 阅读状态
//	@Produce		json
//	@Param			id	path		int			true	"条目 ID"
//	@Success		200	{object}	Response	"成功"
//	@Failure		404	{object}	Response	"条目不存在"
//	@Router			/rss/entries/{id}/star [delete]
func RSSUnstarEntry(c *gin.Context) {
	rssSetEntryState(c, func(u, e uint64) error { return svc.RSS.SetEntryStarred(u, e, false) })
}

// RSSMarkEntriesRead 批量标记已读。
//
//	@Summary		批量标记已读
//	@Description	按源、分组、条目 ID 与发布时间上限（AND 组合）批量标记已读；全部省略即全部已读。返回本次新标记的条数
//	@Tags			RSS 阅读状态
//	@Accept			json
//	@Produce		json
//	@Param			request	body		markEntriesReadRequest	false	"范围"
//	@Success		200		{object}	Response{data=object}	"{marked: n}"
//	@Failure		400		{object}	Response				"参数错误"
//	@Router			/rss/entries/mark-read [post]
func RSSMarkEntriesRead(c *gin.Context) {
	var req markEntriesReadRequest
	// 允许空 body：等价于 {}，即全部已读。
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			FailBadRequest(c, "无效的请求参数（before 需为 RFC3339）")
			return
		}
	}
	userID := getCurrentUserID(c)
	if userID == 0 {
		FailUnauthorized(c, "未登录")
		return
	}
	n, err := svc.RSS.MarkEntriesRead(userID, service.MarkReadInput{
		FeedID: req.FeedID, GroupID: req.GroupID, EntryIDs: req.EntryIDs, Before: req.Before,
	})
	if err != nil {
		if errors.Is(err, service.ErrMarkReadEmptyIDs) || errors.Is(err, service.ErrMarkReadTooMany) {
			FailBadRequest(c, err.Error())
			return
		}
		FailInternal(c, "批量标记已读失败")
		return
	}
	Success(c, gin.H{"marked": n})
}

// RSSUnreadCounts 返回当前用户的未读计数。
//
//	@Summary		未读计数
//	@Description	一次返回总未读数、按源与按分组的未读数（键为 ID 字符串，0 不出现）
//	@Tags			RSS 阅读状态
//	@Produce		json
//	@Success		200	{object}	Response{data=service.UnreadCounts}	"计数"
//	@Router			/rss/unread-counts [get]
func RSSUnreadCounts(c *gin.Context) {
	userID := getCurrentUserID(c)
	if userID == 0 {
		FailUnauthorized(c, "未登录")
		return
	}
	counts, err := svc.RSS.UnreadCounts(userID)
	if err != nil {
		FailInternal(c, "统计未读数失败")
		return
	}
	Success(c, counts)
}
