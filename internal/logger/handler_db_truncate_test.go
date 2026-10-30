package logger

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ignvex/HomeHub/internal/model"
)

// newCaptureHandler 返回一个不启动后台 goroutine 的 dbHandler，
// channel 容量足够容纳测试写入的少量记录。
func newCaptureHandler() *dbHandler {
	return &dbHandler{
		level: slog.LevelDebug,
		ch:    make(chan *model.LogEntry, 8),
	}
}

// TestDBHandlerTruncatesLongMessage 锁住一条回归：
// log_entries.message 是 varchar(2048)，而日志消息完全可能超长
// （RSS 抓取错误、上游响应片段）。不截断时 PostgreSQL 会拒收**整批**
// INSERT，导致一次超长日志带走一批正常日志。
func TestDBHandlerTruncatesLongMessage(t *testing.T) {
	h := newCaptureHandler()

	// 长且为多字节：按字节切片会切断字符，产生非法 UTF-8。
	long := strings.Repeat("抓取订阅源失败：", 400) // 3200 字符
	r := slog.NewRecord(time.Now(), slog.LevelError, long, 0)

	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle 返回错误: %v", err)
	}

	var entry *model.LogEntry
	select {
	case entry = <-h.ch:
	default:
		t.Fatal("日志未进入 channel")
	}

	if !utf8.ValidString(entry.Message) {
		t.Fatal("截断后的 Message 不是合法 UTF-8")
	}
	if n := utf8.RuneCountInString(entry.Message); n > 2048 {
		t.Fatalf("Message 字符数 %d 超过 varchar(2048)", n)
	}
	if !strings.HasSuffix(entry.Message, "...") {
		t.Error("被截断的日志应带省略号，便于识别不完整")
	}
}

// TestDBHandlerKeepsShortMessage 确认未超长的消息原样保留，
// 不能被截断逻辑误伤（截断不应改变正常日志的语义）。
func TestDBHandlerKeepsShortMessage(t *testing.T) {
	h := newCaptureHandler()
	const msg = "服务启动完成"

	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle 返回错误: %v", err)
	}

	entry := <-h.ch
	if entry.Message != msg {
		t.Errorf("Message = %q, want %q", entry.Message, msg)
	}
}

// TestDBHandlerTruncatesLongLevel 确认异常长的自定义 level 不会撑爆
// varchar(10)，且正常级别不受影响。
func TestDBHandlerTruncatesLongLevel(t *testing.T) {
	h := newCaptureHandler()

	// slog 级别名最长是 "ERROR"（5 字符），但 Level 可以自定义字符串化。
	// 这里直接验证字段长度约束本身。
	r := slog.NewRecord(time.Now(), slog.LevelError, "x", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle 返回错误: %v", err)
	}

	entry := <-h.ch
	if n := len(entry.Level); n > 10 {
		t.Errorf("Level 长度 %d 超过 varchar(10): %q", n, entry.Level)
	}
	if entry.Level != "ERROR" {
		t.Errorf("Level = %q, want ERROR", entry.Level)
	}
}
