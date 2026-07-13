package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 系统日志查看 API（管理员）
// ============================================================

// LogList 分页查看系统日志。
//
// GET /api/v1/admin/logs?page=&per_page=&level=&q=&since=&until=
func LogList(c *gin.Context) {
	page := queryInt(c, "page", 1)
	perPage := queryInt(c, "per_page", 50)
	level := c.Query("level")
	q := c.Query("q")

	var since, until time.Time
	if s := c.Query("since"); s != "" {
		since, _ = time.Parse(time.RFC3339, s)
	}
	if u := c.Query("until"); u != "" {
		until, _ = time.Parse(time.RFC3339, u)
	}

	logs, total, err := svc.Log.ListLogs(service.LogQuery{
		Page: page, PerPage: perPage, Level: level, Q: q, Since: since, Until: until,
	})
	if err != nil {
		FailInternal(c, "查询日志失败")
		return
	}
	Success(c, gin.H{"list": logs, "total": total, "page": page, "per_page": perPage})
}
