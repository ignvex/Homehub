package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
)

// LogStream SSE 实时日志推送端点。
//
//	@Summary		实时日志推送
//	@Description	通过 Server-Sent Events 实时推送系统日志（仅管理员）
//	@Tags			系统日志
//	@Produce		text/event-stream
//	@Success		200	{object}	string	"SSE 事件流"
//	@Router			/admin/logs/stream [get]
func LogStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	listener := logger.Subscribe()
	defer logger.Unsubscribe(listener)

	// 服务关闭时 server 会取消所有请求的根 context（见 server.serve），
	// 长连接流靠它及时结束，不拖住 Shutdown。
	ctx := c.Request.Context()
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	// 立即发出响应头。否则在第一条日志到来之前客户端一直收不到任何字节，
	// 反向代理会把它当作上游无响应而超时断开。
	c.Status(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-listener.C():
			if !ok {
				return
			}
			data, err := json.Marshal(toLogEntryJSON(entry))
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// logEntryJSON SSE 推送用的精简日志 JSON。
//
// LogTime 保持 time.Time 而不是格式化成字符串：列表 API 直接序列化
// model.LogEntry，编码结果是 RFC3339。手写格式串（曾用
// "2006-01-02T15:04:05"）会丢掉时区，前端按浏览器本地时区解析后
// 时间整体偏移。交给 encoding/json 就天然与列表接口一致。
type logEntryJSON struct {
	ID      uint64    `json:"id"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Source  string    `json:"source"`
	Fields  string    `json:"fields,omitempty"`
	LogTime time.Time `json:"log_time"`
}

func toLogEntryJSON(e *model.LogEntry) logEntryJSON {
	return logEntryJSON{
		ID:      e.ID,
		Level:   e.Level,
		Message: e.Message,
		Source:  e.Source,
		Fields:  e.Fields,
		LogTime: e.LogTime,
	}
}
