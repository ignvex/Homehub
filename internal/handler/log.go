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
//	@Summary		查看系统日志
//	@Description	分页查看系统运行日志，支持按级别、关键词、时间范围筛选（仅管理员）
//	@Tags			系统日志
//	@Produce		json
//	@Param			page		query		int		false	"页码（默认 1）"
//	@Param			per_page	query		int		false	"每页数量（默认 50）"
//	@Param			level		query		string	false	"日志级别筛选：debug/info/warn/error"
//	@Param			q			query		string	false	"搜索关键词"
//	@Param			since		query		string	false	"起始时间（RFC3339 格式）"
//	@Param			until		query		string	false	"截止时间（RFC3339 格式）"
//	@Success		200			{object}	Response	"日志列表（含分页信息）"
//	@Failure		500			{object}	Response	"服务器内部错误"
//	@Router			/admin/logs [get]
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

// LogClearAll 清除全部系统日志。
//
//	@Summary		清除全部日志
//	@Description	删除所有系统日志记录（仅管理员）
//	@Tags			系统日志
//	@Produce		json
//	@Success		200	{object}	Response	"清除成功"
//	@Failure		500	{object}	Response	"服务器内部错误"
//	@Router			/admin/logs [delete]
func LogClearAll(c *gin.Context) {
	if err := svc.Log.DeleteAll(); err != nil {
		FailInternal(c, "清除日志失败")
		return
	}
	Success(c, nil)
}
