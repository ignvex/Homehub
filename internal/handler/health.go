package handler

import (
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
// GET /api/v1/health
func HealthCheck(c *gin.Context) {
	dbStatus := "ok"
	if err := database.Ping(); err != nil {
		dbStatus = "unreachable"
	}

	c.JSON(200, gin.H{
		"status":         "ok",
		"db":             dbStatus,
		"uptime_seconds": int(time.Since(startTime).Seconds()),
	})
}
