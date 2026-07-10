package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/logger"
)

// Logger 返回一个 Gin 中间件，记录每个 HTTP 请求的关键信息。
//
// 记录字段：method、path、status、latency、client_ip。
// 日志级别统一使用 info，由 internal/logger 包输出。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		logger.Info("HTTP 请求",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency", latency.String(),
			"ip", c.ClientIP(),
		)
	}
}
