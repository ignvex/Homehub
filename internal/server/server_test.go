package server

import (
	"net/http"
	"testing"
)

// TestHTTPServerTimeouts 锁住服务器超时的取舍。
//
// 没有 ReadHeaderTimeout 时，一个慢慢发请求头的客户端就能一直占着连接；
// 而 WriteTimeout 一旦被"顺手"加上，日志 SSE 流会在到时后被服务端掐断。
func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 {
		t.Error("必须设置 ReadHeaderTimeout")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("应设置 IdleTimeout，避免空闲 keep-alive 连接无限保留")
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v，SSE 长连接要求不设", srv.WriteTimeout)
	}
}
