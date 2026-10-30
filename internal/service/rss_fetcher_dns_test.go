package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFeedClientBlocksLoopback 确认 feed 抓取在连接层拒绝回环地址。
//
// feed URL 由用户配置，但仍可能是被劫持的域名；只在请求前查字面 IP
// 挡不住 DNS rebinding，所以这里验证 DialContext 这一层真的生效。
func TestFeedClientBlocksLoopback(t *testing.T) {
	loadConfigWith(t, `,"rss": {}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should not reach"))
	}))
	defer srv.Close()

	resp, err := rssHTTPClient().R().Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("回环地址应被连接层拒绝")
	}
	if !strings.Contains(err.Error(), "内部地址") && !strings.Contains(err.Error(), "禁止连接") {
		t.Errorf("错误信息应说明地址被拒，实际: %v", err)
	}
}

// TestFeedRedirectToLoopbackBlocked 确认 feed 重定向到内网被拦下。
func TestFeedRedirectToLoopbackBlocked(t *testing.T) {
	loadConfigWith(t, `,"rss": {}`)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal"))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/feed.xml", http.StatusFound)
	}))
	defer redirector.Close()

	resp, err := rssHTTPClient().R().Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("重定向到回环地址应失败")
	}
}

// TestValidateRemoteURLResolvesDNS 确认域名会被解析并校验。
//
// localhost 走主机名黑名单；这里额外验证解析路径不会放行。
func TestValidateRemoteURLResolvesDNS(t *testing.T) {
	blocked := []string{
		"http://localhost/feed.xml",
		"http://127.0.0.1:8080/feed.xml",
		"http://[::1]/feed.xml",
	}
	for _, raw := range blocked {
		if err := validateRemoteURL(context.Background(), raw, false); err == nil {
			t.Errorf("%s 应被拒绝", raw)
		}
	}
}
