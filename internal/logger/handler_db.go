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
	db        *gorm.DB
	level     slog.Level
	ch        chan *model.LogEntry
	done      chan struct{}
	closeOnce sync.Once
}

const (
	dbWriterBatchSize     = 100             // 批量插入阈值
	dbWriterFlushInterval = 3 * time.Second // 定时刷新间隔
	dbWriterChannelSize   = 2048            // channel 缓冲区大小
)

// newDBHandler 创建数据库日志 handler 并启动后台写入 goroutine。
func newDBHandler(db *gorm.DB, level slog.Level) *dbHandler {
	h := &dbHandler{
		db:    db,
		level: level,
		ch:    make(chan *model.LogEntry, dbWriterChannelSize),
		done:  make(chan struct{}),
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
		if a.Key == "source" && src == "" {
			// 若便捷函数已通过属性传入真实调用方 source，优先使用
			if s, ok := a.Value.Any().(string); ok {
				entry.Source = s
			}
			return true
		}
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

	// 推送到实时广播（SSE/WebSocket）
	broadcastLog(entry)

	return nil
}

// WithAttrs 返回带预置属性的新 handler，attrs 会在每条日志 Handle 时自动附加。
func (h *dbHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &dbHandlerWithAttrs{parent: h, attrs: attrs}
}

// dbHandlerWithAttrs 带预置属性的 DB handler 包装器。
type dbHandlerWithAttrs struct {
	parent *dbHandler
	attrs  []slog.Attr
}

func (h *dbHandlerWithAttrs) Enabled(ctx context.Context, level slog.Level) bool {
	return h.parent.Enabled(ctx, level)
}

func (h *dbHandlerWithAttrs) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(h.attrs...)
	return h.parent.Handle(ctx, r)
}

func (h *dbHandlerWithAttrs) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &dbHandlerWithAttrs{parent: h.parent, attrs: newAttrs}
}

func (h *dbHandlerWithAttrs) WithGroup(name string) slog.Handler {
	return h.parent.WithGroup(name)
}

// WithGroup 返回带分组的新 handler，group 名称存储在日志字段命名中。
func (h *dbHandler) WithGroup(name string) slog.Handler {
	return &dbHandlerWithGroup{parent: h, group: name}
}

// dbHandlerWithGroup 带分组的 DB handler 包装器。
type dbHandlerWithGroup struct {
	parent *dbHandler
	group  string
}

func (h *dbHandlerWithGroup) Enabled(ctx context.Context, level slog.Level) bool {
	return h.parent.Enabled(ctx, level)
}

func (h *dbHandlerWithGroup) Handle(ctx context.Context, r slog.Record) error {
	return h.parent.Handle(ctx, r)
}

func (h *dbHandlerWithGroup) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &dbHandlerWithGroup{parent: h.parent, group: h.group}
}

func (h *dbHandlerWithGroup) WithGroup(name string) slog.Handler {
	return &dbHandlerWithGroup{parent: h.parent, group: name}
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
