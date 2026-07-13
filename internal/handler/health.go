package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/database"
)

// ============================================================
// 健康检查 API
// ============================================================

var startTime = time.Now()

// HealthCheck 返回服务健康状态。
//
//	@Summary		健康检查
//	@Description	返回服务运行状态、数据库连通性和启动时长
//	@Tags			系统
//	@Produce		json
//	@Success		200	{object}	object	"服务健康状态"
//	@Router			/health [get]
func HealthCheck(c *gin.Context) {
	dbStatus := "ok"
	if err := database.Ping(); err != nil {
		dbStatus = "unreachable"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"db":             dbStatus,
		"uptime_seconds": int(time.Since(startTime).Seconds()),
	})
}
