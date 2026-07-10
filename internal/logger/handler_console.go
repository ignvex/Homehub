package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"

	"github.com/mattn/go-isatty"
)

// ANSI 颜色码。
const (
	colorReset  = "\033[0m"
	colorGray   = "\033[90m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

// levelMeta 包含日志级别的显示字符串和颜色。
type levelMeta struct {
	label string
	color string
}

var levelMap = map[slog.Level]levelMeta{
	slog.LevelDebug: {"DBG", colorGray},
	slog.LevelInfo:  {"INF", colorGreen},
	slog.LevelWarn:  {"WRN", colorYellow},
	slog.LevelError: {"ERR", colorRed},
}

// consoleHandler 是一个自定义 slog.Handler，输出美化的彩色文本日志。
//
// 格式：HH:MM:SS.mmm LEVEL source message key=value ...
// 非 TTY 环境下自动去除 ANSI 颜色码。
type consoleHandler struct {
	opts    slog.HandlerOptions
	w       io.Writer
	colored bool
	mu      sync.Mutex
	goas    []groupOrAttrs // 累积的 WithGroup + WithAttrs
}

type groupOrAttrs struct {
	group string
	attrs []slog.Attr
}

// newConsoleHandler 创建控制台 handler。
//
// colored 控制是否启用 ANSI 颜色。若为 true 但输出目标不是 TTY，
// 自动降级为无颜色输出。
func newConsoleHandler(w io.Writer, opts *slog.HandlerOptions, colored bool) *consoleHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}

	// 自动检测 TTY
	if colored {
		if f, ok := w.(*os.File); ok {
			colored = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
		} else {
			colored = false
		}
	}

	return &consoleHandler{
		opts:    *opts,
		w:       w,
		colored: colored,
	}
}

// Enabled 判断是否应记录该级别日志。
func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle 格式化并输出一条日志记录。
func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 时间戳
	ts := r.Time.Format("15:04:05.000")

	// 级别
	meta, ok := levelMap[r.Level]
	if !ok {
		meta = levelMeta{"LOG", colorReset}
	}

	// 调用源
	src := ""
	if h.opts.AddSource && r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			src = fmt.Sprintf("%s:%d", trimPath(f.File), f.Line)
		}
	}

	// 构建属性列表：预置属性 + 记录属性
	var buf []byte
	buf = appendAttrs(buf, "", h.collectAttrs())
	r.Attrs(func(a slog.Attr) bool {
		buf = appendAttr(buf, "", a)
		return true
	})

	// 组装输出行
	var line string
	if h.colored {
		levelStr := meta.color + meta.label + colorReset
		if src != "" {
			line = fmt.Sprintf("%s %s %s%s%s %s%s\n",
				ts, levelStr,
				colorCyan, src, colorReset,
				r.Message,
				string(buf),
			)
		} else {
			line = fmt.Sprintf("%s %s %s%s\n",
				ts, levelStr,
				r.Message,
				string(buf),
			)
		}
	} else {
		if src != "" {
			line = fmt.Sprintf("%s %s %s %s%s\n",
				ts, meta.label, src, r.Message, string(buf),
			)
		} else {
			line = fmt.Sprintf("%s %s %s%s\n",
				ts, meta.label, r.Message, string(buf),
			)
		}
	}

	_, err := fmt.Fprint(h.w, line)
	return err
}

// WithAttrs 返回附加了指定属性的新 handler。
func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h2 := h.clone()
	h2.goas = append(h2.goas, groupOrAttrs{attrs: attrs})
	return h2
}

// WithGroup 返回将后续属性归入指定组的新 handler。
func (h *consoleHandler) WithGroup(name string) slog.Handler {
	h2 := h.clone()
	h2.goas = append(h2.goas, groupOrAttrs{group: name})
	return h2
}

// clone 深拷贝 handler（不含 mu）。
func (h *consoleHandler) clone() *consoleHandler {
	goasCopy := make([]groupOrAttrs, len(h.goas))
	copy(goasCopy, h.goas)
	return &consoleHandler{
		opts:    h.opts,
		w:       h.w,
		colored: h.colored,
		goas:    goasCopy,
	}
}

// collectAttrs 汇集所有累积的属性。
func (h *consoleHandler) collectAttrs() []slog.Attr {
	var result []slog.Attr
	for _, ga := range h.goas {
		for _, a := range ga.attrs {
			result = append(result, a)
		}
	}
	return result
}

// appendAttr 将单个 slog.Attr 格式化为 key=value 追加到 buf。
func appendAttr(buf []byte, prefix string, a slog.Attr) []byte {
	if a.Equal(slog.Attr{}) {
		return buf
	}
	if buf == nil {
		buf = make([]byte, 0, 256)
	}
	if len(buf) > 0 || prefix != "" {
		buf = append(buf, ' ')
	}
	if prefix != "" {
		buf = append(buf, prefix...)
		buf = append(buf, '.')
	}
	buf = append(buf, a.Key...)
	buf = append(buf, '=')
	buf = append(buf, a.Value.String()...)
	return buf
}

// appendAttrs 批量追加属性。
func appendAttrs(buf []byte, prefix string, attrs []slog.Attr) []byte {
	for _, a := range attrs {
		buf = appendAttr(buf, prefix, a)
	}
	return buf
}

// trimPath 缩短源文件路径：仅保留最后两级目录 + 文件名。
func trimPath(path string) string {
	// 查找倒数第二个路径分隔符
	idx := len(path) - 1
	sepCount := 0
	for i := idx; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			sepCount++
			if sepCount == 2 {
				return path[i+1:]
			}
		}
	}
	return path
}
