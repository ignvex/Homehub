package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

// TestClientIPIgnoresForwardedHeadersByDefault 防的是 Gin「默认信任所有代理」。
//
// 未配置 trusted_proxies 时，客户端伪造的 X-Forwarded-For 若被采纳，
// 换一个头就能绕开按 IP 的限流与登录锁定。
func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	cases := []struct {
		name    string
		proxies []string
		want    string
	}{
		{"未配置时只认直连地址", nil, "203.0.113.7"},
		{"配置的代理之外的来源也不认", []string{"10.0.0.1"}, "203.0.113.7"},
		{"来自可信代理时采用转发头", []string{"203.0.113.0/24"}, "198.51.100.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newEngine(tc.proxies)
			if err != nil {
				t.Fatalf("newEngine: %v", err)
			}
			var got string
			r.GET("/ip", func(c *gin.Context) { got = c.ClientIP() })
			req := httptest.NewRequest(http.MethodGet, "/ip", nil)
			req.RemoteAddr = "203.0.113.7:4321"
			req.Header.Set("X-Forwarded-For", "198.51.100.9")
			req.Header.Set("X-Real-IP", "198.51.100.9")
			r.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNewEngineRejectsInvalidProxy 确认非法代理地址让启动失败而不是被忽略。
func TestNewEngineRejectsInvalidProxy(t *testing.T) {
	if _, err := newEngine([]string{"not-an-ip"}); err == nil {
		t.Error("非法 trusted_proxies 应返回错误")
	}
}
