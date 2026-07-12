package handler

import "github.com/gin-gonic/gin"

// ============================================================
// 搜索 API
// ============================================================

// SearchAll 跨书签和 B 站数据的联合搜索。
//
// GET /api/v1/search?q=关键词&scope=all|bookmarks|videos|articles|users
//
// scope 默认 "all"，可指定单个数据源。
func SearchAll(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		FailBadRequest(c, "请提供搜索关键词 q")
		return
	}

	scope := c.DefaultQuery("scope", "all")

	result, err := svc.Search.Search(q, scope)
	if err != nil {
		FailInternal(c, "搜索失败: "+err.Error())
		return
	}

	Success(c, result)
}
