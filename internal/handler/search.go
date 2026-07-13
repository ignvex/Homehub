package handler

import "github.com/gin-gonic/gin"

// ============================================================
// 搜索 API
// ============================================================

// SearchAll 跨书签和 B 站数据的联合搜索。
//
//	@Summary		联合搜索
//	@Description	跨书签和 Bilibili 爬虫数据（视频/专栏/用户）进行全文搜索，支持指定数据源范围
//	@Tags			搜索
//	@Produce		json
//	@Param			q		query		string	true	"搜索关键词"
//	@Param			scope	query		string	false	"搜索范围：all / bookmarks / videos / articles / users（默认 all）"
//	@Success		200		{object}	Response	"搜索结果"
//	@Failure		400		{object}	Response	"缺少搜索关键词"
//	@Failure		500		{object}	Response	"服务器内部错误"
//	@Router			/search [get]
func SearchAll(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		FailBadRequest(c, "请提供搜索关键词 q")
		return
	}

	scope := c.DefaultQuery("scope", "all")

	result, err := svc.Search.Search(q, scope)
	if err != nil {
		FailInternal(c, "搜索失败")
		return
	}

	Success(c, result)
}
