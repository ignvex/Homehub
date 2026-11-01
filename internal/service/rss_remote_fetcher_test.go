package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRemoteFetcherParsesXML 确认 extension 返回原始 XML 时能解析。
func TestRemoteFetcherParsesXML(t *testing.T) {
	const rssXML = `<?xml version="1.0"?>
<rss version="2.0"><channel><title>remote</title>
<item><title>a</title><link>http://example.com/a</link></item>
</channel></rss>`

	var gotPath, gotQuery, gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		gotQuery.Store(r.URL.Query().Get("url"))
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssXML))
	}))
	defer srv.Close()

	loadConfigWith(t, fmt.Sprintf(`,"rss": {"extension_url": %q, "extension_token": "secret"}`, srv.URL))

	f := NewRemoteFetcher(0)
	feed, err := f.Fetch(context.Background(), "http://example.com/feed.xml")
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if feed.Title != "remote" {
		t.Errorf("title = %q，期望 remote", feed.Title)
	}
	if len(feed.Items) != 1 || feed.Items[0].Title != "a" {
		t.Errorf("条目解析异常: %+v", feed.Items)
	}

	// 通信方向：HomeHub → extension，走 GET /fetch?url=
	if gotPath.Load() != "/fetch" {
		t.Errorf("请求路径 = %v，期望 /fetch", gotPath.Load())
	}
	if gotQuery.Load() != "http://example.com/feed.xml" {
		t.Errorf("url 参数 = %v", gotQuery.Load())
	}
	if gotAuth.Load() != "Bearer secret" {
		t.Errorf("Authorization = %v，期望 Bearer secret", gotAuth.Load())
	}
}

// TestRemoteFetcherParsesJSONWrapper 确认 {"feed": "<xml>"} 形态可用。
func TestRemoteFetcherParsesJSONWrapper(t *testing.T) {
	const rssXML = `<?xml version="1.0"?><rss version="2.0"><channel><title>wrapped</title></channel></rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"feed":%q}`, rssXML)
	}))
	defer srv.Close()

	loadConfigWith(t, fmt.Sprintf(`,"rss": {"extension_url": %q}`, srv.URL))

	feed, err := NewRemoteFetcher(0).Fetch(context.Background(), "http://example.com/f")
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if feed.Title != "wrapped" {
		t.Errorf("title = %q，期望 wrapped", feed.Title)
	}
}

// TestRemoteFetcherErrorStatus 确认非 2xx 返回带状态码的错误。
func TestRemoteFetcherErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	defer srv.Close()

	loadConfigWith(t, fmt.Sprintf(`,"rss": {"extension_url": %q}`, srv.URL))

	_, err := NewRemoteFetcher(0).Fetch(context.Background(), "http://example.com/f")
	if err == nil {
		t.Fatal("502 应返回错误")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("错误信息应包含状态码，实际: %v", err)
	}
}

// TestRemoteFetcherWithoutURLFails 确认未配置地址时明确报错。
func TestRemoteFetcherWithoutURLFails(t *testing.T) {
	loadConfigWith(t, `,"rss": {}`)

	_, err := NewRemoteFetcher(0).Fetch(context.Background(), "http://example.com/f")
	if err == nil {
		t.Fatal("未配置 extension_url 应报错")
	}
}

// TestAutoFetcherFollowsConfig 确认抓取器随配置在 local / remote 间切换。
//
// 配置热更新后不应需要重启：改完 extension_url 下次抓取就该走新实现。
func TestAutoFetcherFollowsConfig(t *testing.T) {
	a := newAutoFetcher()

	// 初始为本地抓取
	loadConfigWith(t, `,"rss": {}`)
	if _, ok := a.pick().(httpFeedFetcher); !ok {
		t.Errorf("无 extension_url 时应本地抓取，实际 %T", a.pick())
	}

	// 指向 extension 后切换
	loadConfigWith(t, `,"rss": {"extension_url": "http://ext.test:8090"}`)
	if _, ok := a.pick().(*remoteFetcher); !ok {
		t.Errorf("配置 extension_url 后应走 remoteFetcher，实际 %T", a.pick())
	}

	// 清空后回到本地
	loadConfigWith(t, `,"rss": {}`)
	if _, ok := a.pick().(httpFeedFetcher); !ok {
		t.Errorf("清空 extension_url 后应回到本地抓取，实际 %T", a.pick())
	}
}

// TestRemoteFetcherRejectsHTMLEndpoint 确认配错 extension_url 时给出准确错误。
//
// 这是真实踩到的：extension_url 指向一个没有 /fetch 的普通站点时，对方
// 用 200 返回首页 HTML，而 gofeed 的 "failed to detect feed type" 会让人
// 以为订阅源本身有问题——实际是地址配错了。
func TestRemoteFetcherRejectsHTMLEndpoint(t *testing.T) {
	html := `<!doctype html><html lang="zh-CN"><head><title>Homehub</title></head><body></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	loadConfigWith(t, fmt.Sprintf(`,"rss": {"extension_url": %q}`, srv.URL))

	fetcher := NewRemoteFetcher(5 * time.Second)
	_, err := fetcher.Fetch(context.Background(), "https://example.com/feed.xml")
	if err == nil {
		t.Fatal("HTML 响应必须被拒绝")
	}
	msg := err.Error()
	if !strings.Contains(msg, "HTML") {
		t.Fatalf("错误应点明返回的是 HTML，实际 %q", msg)
	}
	if !strings.Contains(msg, "/fetch") {
		t.Fatalf("错误应指向 /fetch 端点，实际 %q", msg)
	}
	if strings.Contains(msg, "detect feed type") {
		t.Fatalf("不应把 gofeed 的原始报错抛给用户，实际 %q", msg)
	}
}

// TestRemoteFetcherRejectsHTMLEvenWithWrongContentType 确认只靠嗅探也能拦住。
//
// 有些服务的 fallback 会把 HTML 标成 application/octet-stream；
// 只看声明会漏过去。
func TestRemoteFetcherRejectsHTMLEvenWithWrongContentType(t *testing.T) {
	html := `<!doctype html><html><body>oops</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	loadConfigWith(t, fmt.Sprintf(`,"rss": {"extension_url": %q}`, srv.URL))

	fetcher := NewRemoteFetcher(5 * time.Second)
	_, err := fetcher.Fetch(context.Background(), "https://example.com/feed.xml")
	if err == nil {
		t.Fatal("声明错误的 HTML 也必须被拦住")
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("错误应点明 HTML，实际 %q", err)
	}
}
