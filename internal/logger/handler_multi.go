package logger

import (
	"context"
	"log/slog"
)

// multiHandler 将多条日志记录扇出到多个 slog.Handler。
//
// 每个 handler 独立处理自己的日志记录副本（Clone 防止状态污染）。
// Enabled 采用乐观策略：任意一个 handler 启用即记录。
type multiHandler struct {
	handlers []slog.Handler
}

// Enabled 若任一子 handler 启用该级别，则返回 true。
func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle 将日志记录分发给每个子 handler（各自 Clone 独立副本）。
func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for i, handler := range h.handlers {
		var r2 slog.Record
		if i < len(h.handlers)-1 {
			r2 = r.Clone()
		} else {
			r2 = r // 最后一个无需 Clone
		}
		if handler.Enabled(ctx, r2.Level) {
			if err := handler.Handle(ctx, r2); err != nil {
				return err
			}
		}
	}
	return nil
}

// WithAttrs 返回所有子 handler 均附加属性的新 multiHandler。
func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

// WithGroup 返回所有子 handler 均归入指定组的新 multiHandler。
func (h *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
}
