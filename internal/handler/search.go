package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 搜索 API
// ============================================================

// SearchAll 跨书签和 RSS 条目的联合搜索。
//
//	@Summary		联合搜索
//	@Description	在书签（标题/URL/分组名）与 RSS 条目（标题/正文/作者）中搜索，支持分页、按源与分组过滤
//	@Tags			搜索
//	@Produce		json
//	@Param			q			query		string	true	"搜索关键词"
//	@Param			scope		query		string	false	"搜索范围：all / bookmarks / rss（默认 all）"
//	@Param			page		query		int		false	"页码，从 1 开始（默认 1）"
//	@Param			per_page	query		int		false	"每页条数（默认 20，上限 100）"
//	@Param			feed_id		query		int		false	"只搜该订阅源收录的条目"
//	@Param			group_id	query		int		false	"只搜该书签分组下的条目"
//	@Success		200			{object}	Response{data=service.SearchResult}
//	@Failure		400			{object}	Response	"缺少搜索关键词或范围不合法"
//	@Failure		401			{object}	Response	"未登录"
//	@Failure		500			{object}	Response	"服务器内部错误"
//	@Router			/search [get]
func SearchAll(c *gin.Context) {
	opts := service.SearchOptions{
		Query:   c.Query("q"),
		Scope:   c.DefaultQuery("scope", "all"),
		Page:    queryInt(c, "page", 1),
		PerPage: queryInt(c, "per_page", service.DefaultSearchPerPage),
		FeedID:  queryUint64(c, "feed_id"),
		GroupID: queryUint64(c, "group_id"),
	}

	result, err := svc.Search.Search(opts)
	if err != nil {
		// 关键词为空、范围不认识属于调用方能自己改的输入，回 400；
		// 其余（SQL 失败、超时）才是服务端问题。
		if isSearchBadRequest(err) {
			FailBadRequest(c, err.Error())
			return
		}
		// 驱动错误会带表名、列名甚至语句片段，不回给客户端。
		logger.Error("搜索失败", "error", err, "scope", opts.Scope)
		FailInternal(c, "搜索失败")
		return
	}

	Success(c, result)
}

// isSearchBadRequest 判断错误是否由调用方输入引起。
//
// 用哨兵错误而不是比较文本：文案会改，状态码不该跟着改。
// 误判的方向也很糟——本该 400 的变成 500，会把一次输入错误
// 报成服务端故障。
func isSearchBadRequest(err error) bool {
	return errors.Is(err, service.ErrSearchEmptyQuery) ||
		errors.Is(err, service.ErrSearchBadScope)
}

// queryUint64 读取无符号整数查询参数，缺失或非法时返回 0（表示不过滤）。
func queryUint64(c *gin.Context, key string) uint64 {
	raw := c.Query(key)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
