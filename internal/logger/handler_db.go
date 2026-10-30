package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
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
		// 批量插入，错误不交回主流程（日志写日志会递归）。
		// 但失败必须留下痕迹：这批日志会彻底丢失，静默丢弃会让
		// "日志里没报错"被误读为"没出错"。此处只能写 stderr，
		// 不能调用本包 logger —— 那会把错误再次推进这个 handler。
		if err := h.db.CreateInBatches(batch, dbWriterBatchSize).Error; err != nil {
			fmt.Fprintf(os.Stderr, "[logger] 批量写入 %d 条日志失败，本批已丢弃: %v\n", len(batch), err)
		}
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
		Level: model.TruncateRunes(r.Level.String(), 10),
		// message 是 varchar(2048)：RSS 抓取错误等内容轻松超长，
		// 不截断会让整批 INSERT 被 PostgreSQL 拒收。按字符截断，
		// 避免把多字节字符切成两半产生非法 UTF-8。
		Message: model.TruncateEllipsis(r.Message, 2048),
		Source:  model.TruncateRunes(src, 255),
		LogTime: r.Time,
	}

	// 收集附加字段
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "source" && src == "" {
			// 若便捷函数已通过属性传入真实调用方 source，优先使用
			if s, ok := a.Value.Any().(string); ok {
				entry.Source = model.TruncateRunes(s, 255)
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

// dbHandlerWithAttrs 带预置属性（及可选分组前缀）的 DB handler 包装器。
//
// groups 保存 WithGroup 累积的分组名，按 slog 语义把后续所有属性的
// key 渲染为 "g1.g2.key"。
type dbHandlerWithAttrs struct {
	parent *dbHandler
	attrs  []slog.Attr
	groups []string
}

// prefixKey 按分组层级给属性 key 加前缀。
func (h *dbHandlerWithAttrs) prefixKey(a slog.Attr) slog.Attr {
	if len(h.groups) == 0 {
		return a
	}
	a.Key = strings.Join(h.groups, ".") + "." + a.Key
	return a
}

func (h *dbHandlerWithAttrs) Enabled(ctx context.Context, level slog.Level) bool {
	return h.parent.Enabled(ctx, level)
}

func (h *dbHandlerWithAttrs) Handle(ctx context.Context, r slog.Record) error {
	if len(h.groups) == 0 && len(h.attrs) == 0 {
		return h.parent.Handle(ctx, r)
	}

	// 重建 record，把原有属性与预置属性统一加分组前缀后再交给 parent。
	// 空 Attr 按 slog 语义丢弃（Attr 为零值时 key 为空）。
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if !a.Equal(slog.Attr{}) {
			nr.AddAttrs(h.prefixKey(a))
		}
		return true
	})
	for _, a := range h.attrs {
		if !a.Equal(slog.Attr{}) {
			nr.AddAttrs(h.prefixKey(a))
		}
	}
	return h.parent.Handle(ctx, nr)
}

func (h *dbHandlerWithAttrs) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	combined = append(combined, h.attrs...)
	combined = append(combined, attrs...)
	return &dbHandlerWithAttrs{parent: h.parent, attrs: combined, groups: h.groups}
}

// WithGroup 返回带分组的新 handler，后续属性的 key 会加上分组前缀。
func (h *dbHandlerWithAttrs) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, 0, len(h.groups)+1)
	groups = append(groups, h.groups...)
	groups = append(groups, name)
	return &dbHandlerWithAttrs{parent: h.parent, attrs: h.attrs, groups: groups}
}

// WithGroup 返回带分组的新 handler，后续属性的 key 会加上分组前缀。
func (h *dbHandler) WithGroup(name string) slog.Handler {
	return (&dbHandlerWithAttrs{parent: h}).WithGroup(name)
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
