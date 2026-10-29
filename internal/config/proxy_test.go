package config

import (
	"net/http"
	"testing"
)

// TestProxyURLForScheme 确认 https 请求优先用 https_url，缺失时回落 http_url。
//
// 历史上 https_url 字段存在但代码只读 http_url，配置了完全不生效。
func TestProxyURLForScheme(t *testing.T) {
	p := ProxyEntry{
		HTTPURL:  "http://127.0.0.1:7890",
		HTTPSURL: "http://127.0.0.1:7891",
	}

	if got := p.ProxyURLFor("http"); got != "http://127.0.0.1:7890" {
		t.Errorf("http 请求代理 = %q，期望 http_url", got)
	}
	if got := p.ProxyURLFor("https"); got != "http://127.0.0.1:7891" {
		t.Errorf("https 请求代理 = %q，期望 https_url", got)
	}
}

// TestProxyURLForFallsBack 确认 https_url 为空时回落到 http_url。
//
// 多数 HTTP 代理支持 CONNECT，同一地址可服务两种协议；
// 留空就是"两端用同一个"，不应变成不代理。
func TestProxyURLForFallsBack(t *testing.T) {
	p := ProxyEntry{HTTPURL: "http://127.0.0.1:7890"}

	if got := p.ProxyURLFor("https"); got != "http://127.0.0.1:7890" {
		t.Errorf("https_url 为空时应回落 http_url，实际 %q", got)
	}
}

// TestTransportForNoProxy 确认未配置代理时返回直连 transport。
func TestTransportForNoProxy(t *testing.T) {
	tr, err := TransportFor(nil)
	if err != nil {
		t.Fatalf("TransportFor(nil) 出错: %v", err)
	}
	if tr.Proxy != nil {
		t.Error("无代理时 Proxy 应为 nil（直连）")
	}
}

// TestTransportForHTTP 确认 HTTP 代理按 scheme 分流。
func TestTransportForHTTP(t *testing.T) {
	bak := globalConfig.Load()
	t.Cleanup(func() { globalConfig.Store(bak) })

	cfg := &Config{
		Proxy: []ProxyEntry{{
			Name:     "p",
			Type:     "http",
			HTTPURL:  "http://127.0.0.1:1111",
			HTTPSURL: "http://127.0.0.1:2222",
			Enabled:  true,
		}},
	}
	globalConfig.Store(cfg)

	tr, err := TransportFor([]string{"p"})
	if err != nil {
		t.Fatalf("TransportFor 出错: %v", err)
	}
	if tr.Proxy == nil {
		t.Fatal("配置了代理但 Proxy 为 nil")
	}

	httpReq, _ := http.NewRequest(http.MethodGet, "http://example.com/x", nil)
	u, err := tr.Proxy(httpReq)
	if err != nil || u == nil {
		t.Fatalf("http 请求解析代理失败: %v", err)
	}
	if u.Host != "127.0.0.1:1111" {
		t.Errorf("http 请求代理 = %s，期望 1111", u.Host)
	}

	httpsReq, _ := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	u, err = tr.Proxy(httpsReq)
	if err != nil || u == nil {
		t.Fatalf("https 请求解析代理失败: %v", err)
	}
	if u.Host != "127.0.0.1:2222" {
		t.Errorf("https 请求代理 = %s，期望 2222", u.Host)
	}
}

// TestTransportForSkipsDisabledAndEmpty 确认跳过未启用与空地址的条目。
func TestTransportForSkipsDisabledAndEmpty(t *testing.T) {
	bak := globalConfig.Load()
	t.Cleanup(func() { globalConfig.Store(bak) })

	cfg := &Config{
		Proxy: []ProxyEntry{
			{Name: "off", Type: "http", HTTPURL: "http://127.0.0.1:1", Enabled: false},
			{Name: "empty", Type: "http", Enabled: true},
			{Name: "ok", Type: "http", HTTPURL: "http://127.0.0.1:7890", Enabled: true},
		},
	}
	globalConfig.Store(cfg)

	tr, err := TransportFor([]string{"off", "empty", "ok"})
	if err != nil {
		t.Fatalf("TransportFor 出错: %v", err)
	}
	if tr.Proxy == nil {
		t.Fatal("应选中启用的 ok 代理")
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	u, _ := tr.Proxy(req)
	if u == nil || u.Host != "127.0.0.1:7890" {
		t.Errorf("应跳过 disabled 与空地址，实际代理 = %v", u)
	}
}

// TestTransportForSocks5 确认 socks5 走拨号器而非 Proxy。
func TestTransportForSocks5(t *testing.T) {
	bak := globalConfig.Load()
	t.Cleanup(func() { globalConfig.Store(bak) })

	cfg := &Config{
		Proxy: []ProxyEntry{{
			Name:    "s",
			Type:    "socks5",
			HTTPURL: "127.0.0.1:1080", // socks5 存 host:port
			Enabled: true,
		}},
	}
	globalConfig.Store(cfg)

	tr, err := TransportFor([]string{"s"})
	if err != nil {
		t.Fatalf("TransportFor 出错: %v", err)
	}
	if tr.DialContext == nil {
		t.Error("socks5 应设置 DialContext")
	}
	if tr.Proxy != nil {
		t.Error("socks5 不应设置 Proxy")
	}
}

// TestTransportForInvalidSocks5 确认非法 socks5 地址返回错误而非 panic。
func TestTransportForInvalidSocks5(t *testing.T) {
	bak := globalConfig.Load()
	t.Cleanup(func() { globalConfig.Store(bak) })

	cfg := &Config{
		Proxy: []ProxyEntry{{
			Name:    "bad",
			Type:    "socks5",
			HTTPURL: "http://not-a-host", // 带 scheme，proxy.SOCKS5 会拒绝
			Enabled: true,
		}},
	}
	globalConfig.Store(cfg)

	if _, err := TransportFor([]string{"bad"}); err == nil {
		t.Error("非法 socks5 地址应返回错误")
	}
}
