package logger

import (
	"sync"

	"github.com/ignvex/HomeHub/internal/model"
)

// ============================================================
// 日志广播 — SSE/WebSocket 实时推送支持
// ============================================================

// LogListener 日志监听器，接收新日志条目后通过 channel 推送。
type LogListener struct {
	ch chan *model.LogEntry
}

func newLogListener() *LogListener {
	return &LogListener{ch: make(chan *model.LogEntry, 256)}
}

// C 返回只读 channel。
func (l *LogListener) C() <-chan *model.LogEntry { return l.ch }

// Close 关闭监听器。
func (l *LogListener) Close() { close(l.ch) }

// ============================================================
// 订阅者管理
// ============================================================

var (
	mu        sync.RWMutex
	listeners = make(map[*LogListener]struct{})
)

// Subscribe 注册日志监听器，dbHandler 写入日志后广播到此 channel。
func Subscribe() *LogListener {
	l := newLogListener()
	mu.Lock()
	listeners[l] = struct{}{}
	mu.Unlock()
	return l
}

// Unsubscribe 注销日志监听器。
func Unsubscribe(l *LogListener) {
	mu.Lock()
	delete(listeners, l)
	mu.Unlock()
	l.Close()
}

// broadcastLog 向所有订阅者非阻塞广播一条新日志条目。
func broadcastLog(entry *model.LogEntry) {
	mu.RLock()
	defer mu.RUnlock()
	for l := range listeners {
		select {
		case l.ch <- entry:
		default:
			// 监听器消费太慢，丢弃
		}
	}
}
