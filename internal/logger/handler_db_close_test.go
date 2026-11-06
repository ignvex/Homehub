package logger

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
)

// TestDBHandlerHandleAfterCloseDoesNotPanic 防的是关停后写日志把进程打崩。
//
// Close 会关闭内部 channel，但关停时仍有 goroutine 不受 WaitGroup 管辖
// （CreateFeed 触发的 FetchNow、config.Watch、等待超时后残留的任务），
// 它们随后写日志会向已关闭的 channel 发送，select 的 default 分支救不了。
// 经 WithAttrs 包装的 handler 走同一条路径，一并覆盖。
func TestDBHandlerHandleAfterCloseDoesNotPanic(t *testing.T) {
	h := newDBHandler(nil, slog.LevelDebug)
	h.Close()

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("Close 之后 Handle panic: %v", p)
		}
	}()

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "after close", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle 返回错误: %v", err)
	}
	wrapped := h.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if err := wrapped.Handle(context.Background(), r); err != nil {
		t.Fatalf("包装 handler 的 Handle 返回错误: %v", err)
	}
}

// TestDBHandlerConcurrentHandleAndClose 用 -race 验证关停与并发写日志之间
// 没有数据竞争，也没有「检查完未关闭、发送前被关闭」的窗口。
//
// 不启动真正的 run（db 为空会在刷盘时出错），用一个只排空 channel 的
// 消费者代替，Close 的关闭与等待语义保持一致。
func TestDBHandlerConcurrentHandleAndClose(t *testing.T) {
	h := &dbHandler{
		level: slog.LevelDebug,
		ch:    make(chan *model.LogEntry, 4),
		done:  make(chan struct{}),
	}
	go func() {
		defer close(h.done)
		for range h.ch {
		}
	}()

	var wg sync.WaitGroup
	panics := make(chan any, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					panics <- p
				}
			}()
			for j := 0; j < 200; j++ {
				_ = h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "x", 0))
			}
		}()
	}
	time.Sleep(time.Millisecond)
	h.Close()
	wg.Wait()
	close(panics)
	for p := range panics {
		t.Fatalf("并发关停时 Handle panic: %v", p)
	}
}
