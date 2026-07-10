package logger

import (
	"context"
	"encoding/json"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/model"

	"gorm.io/gorm"
)

// dbHandler 是一个 slog.Handler 实现，将日志异步写入 PostgreSQL。
//
// 内部使用缓冲 channel + goroutine 批量插入，避免阻塞主流程。
// 缓冲区满时丢弃新日志（不阻塞应用），refreshInterval 到时或 batchSize
// 达到阈值时执行一次批量 INSERT。
type dbHandler struct {
	db      *gorm.DB
	level   slog.Level
	ch      chan *model.LogEntry
	done    chan struct{}
	closeOnce sync.Once
}

const (
	dbWriterBatchSize      = 100             // 批量插入阈值
	dbWriterFlushInterval  = 3 * time.Second // 定时刷新间隔
	dbWriterChannelSize    = 2048            // channel 缓冲区大小
)

// newDBHandler 创建数据库日志 handler 并启动后台写入 goroutine。
func newDBHandler(db *gorm.DB, level slog.Level) *dbHandler {
	h := &dbHandler{
		db:   db,
		level: level,
		ch:   make(chan *model.LogEntry, dbWriterChannelSize),
		done: make(chan struct{}),
	}

	go h.run()

	return h
}

// run 后台写入循环，批量收集日志条目并写入数据库。
func (h *dbHandler) run() {
	defer close(h.done)

	ticker := time.NewTicker(dbWriterFlushInterval)
	defer ticker.Stop()

	batch := make([]*model.LogEntry, 0, dbWriterBatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// 批量插入，忽略错误（避免日志写日志的递归问题）
		_ = h.db.CreateInBatches(batch, dbWriterBatchSize).Error
		batch = batch[:0]
	}

	for {
		select {
		case entry, ok := <-h.ch:
			if !ok {
				// channel 已关闭，刷盘退出
				flush()
				return
			}
			batch = append(batch, entry)
			if len(batch) >= dbWriterBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Enabled 判断是否应记录该级别日志。
func (h *dbHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle 将日志记录转换为 LogEntry 并推入异步 channel。
func (h *dbHandler) Handle(_ context.Context, r slog.Record) error {
	src := ""
	if r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			src = trimPath(f.File) + ":" + itoa(f.Line)
		}
	}

	entry := &model.LogEntry{
		Level:   r.Level.String(),
		Message: r.Message,
		Source:  src,
		LogTime: r.Time,
	}

	// 收集附加字段
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	if len(attrs) > 0 {
		data, _ := json.Marshal(attrs)
		entry.Fields = string(data)
	}

	select {
	case h.ch <- entry:
	default:
		// 缓冲区满，丢弃（不阻塞主流程）
	}

	return nil
}

// WithAttrs 返回附加属性的新 handler（DB handler 不使用预置属性，直接透传）。
func (h *dbHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

// WithGroup 返回按组组织的新 handler（DB handler 不使用分组，直接透传）。
func (h *dbHandler) WithGroup(name string) slog.Handler {
	return h
}

// Close 关闭 DB handler，等待后台写入完成。
func (h *dbHandler) Close() {
	h.closeOnce.Do(func() {
		close(h.ch)
		<-h.done
	})
}

// itoa 快速整数转字符串（小值场景）。
func itoa(n int) string {
	if n < 0 {
		return "-" + uitoa(uint(-n))
	}
	return uitoa(uint(n))
}

func uitoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}
