package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestDownloadRejectsOversizeImage 确认超过上限的图片不会被截断后写坏。
//
// 旧实现用 io.LimitReader(body, 2MB) 写目标文件：超过 2MB 的图片会被
// 截断，但仍然返回成功，磁盘上留下无法解码的文件。这里要求直接失败，
// 且缓存目录里不留下任何 rss_ 文件。
func TestDownloadRejectsOversizeImage(t *testing.T) {
	dir := t.TempDir()
	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}

	body := strings.Repeat("x", 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := c.download(context.Background(), srv.URL+"/big.png", "key", 100, true)
	if err == nil {
		t.Fatal("超过上限的图片应返回错误")
	}
	if !strings.Contains(err.Error(), "超过上限") {
		t.Errorf("错误信息应说明超限，实际: %v", err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("读取缓存目录失败: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "rss_") {
			t.Errorf("超限图片不应留下缓存文件: %s", entry.Name())
		}
	}
}

// TestDownloadRejectsNonImage 确认 HTML 错误页不会被当成图片缓存。
func TestDownloadRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>blocked</body></html>"))
	}))
	defer srv.Close()

	_, err := c.download(context.Background(), srv.URL+"/pic.png", "key", 1<<20, true)
	if err == nil {
		t.Fatal("HTML 响应应被拒绝")
	}
	if !strings.Contains(err.Error(), "不是图片") {
		t.Errorf("错误信息应说明不是图片，实际: %v", err)
	}
}

// TestDownloadAcceptsSVG 确认 SVG（标准库嗅探为 text/xml）不被误杀。
func TestDownloadAcceptsSVG(t *testing.T) {
	dir := t.TempDir()
	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}

	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(svg))
	}))
	defer srv.Close()

	res, err := c.download(context.Background(), srv.URL+"/logo.svg", "svgkey", 1<<20, true)
	if err != nil {
		t.Fatalf("SVG 应被接受: %v", err)
	}
	if res.filename != "rss_svgkey.svg" {
		t.Errorf("文件名 = %q，期望 rss_svgkey.svg", res.filename)
	}
	if _, statErr := os.Stat(filepath.Join(dir, res.filename)); statErr != nil {
		t.Errorf("SVG 文件未落盘: %v", statErr)
	}
}

// TestCacheReusesDownloadedFile 确认同一 URL 第二次调用直接命中文件，
// 不再发起 HTTP 请求。
func TestCacheReusesDownloadedFile(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}
	src := srv.URL + "/a.png"

	first := c.Cache(context.Background(), 1, []string{src})
	if len(first) != 1 {
		t.Fatalf("首次缓存应返回 1 个映射，实际 %v", first)
	}
	second := c.Cache(context.Background(), 2, []string{src})
	if len(second) != 1 {
		t.Fatalf("第二次缓存应命中文件，实际 %v", second)
	}
	if first[src] != second[src] {
		t.Errorf("两次返回的站内 URL 不一致: %q vs %q", first[src], second[src])
	}
	if hits.Load() != 1 {
		t.Errorf("图片请求次数 = %d，期望 1（第二次应命中缓存）", hits.Load())
	}
}

// TestCacheSkipsFailedImages 确认下载失败的 URL 不出现在映射中，
// 调用方据此移除对应 <img> 标签。
func TestCacheSkipsFailedImages(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}
	got := c.Cache(context.Background(), 1, []string{srv.URL + "/missing.png"})
	if len(got) != 0 {
		t.Errorf("失败图片不应返回映射，实际 %v", got)
	}
}

// TestImageContentTypeValidation 覆盖类型判断的关键分支。
func TestImageContentTypeValidation(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		peek        string
		src         string
		want        bool
	}{
		{"png", "image/png", "\x89PNG\r\n\x1a\n", "http://x/a.png", true},
		{"jpeg with charset", "image/jpeg; charset=binary", "", "http://x/a.jpg", true},
		{"html", "text/html", "<html>", "http://x/a.png", false},
		{"svg via xml sniff", "text/xml", `<svg xmlns="http://www.w3.org/2000/svg">`, "http://x/a.svg", true},
		{"xml but not svg", "text/xml", `<?xml version="1.0"?><rss>`, "http://x/a.xml", false},
		{"empty", "", "", "http://x/a.png", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedImageContentType(tc.contentType, []byte(tc.peek), tc.src); got != tc.want {
				t.Errorf("isAllowedImageContentType(%q, %q, %q) = %v，期望 %v", tc.contentType, tc.peek, tc.src, got, tc.want)
			}
		})
	}
}

// TestImageExtMatchesContentType 确认扩展名由 Content-Type 决定，
// 不信任 URL 里可能是 .php 的后缀。
func TestImageExtMatchesContentType(t *testing.T) {
	cases := []struct {
		contentType string
		src         string
		want        string
	}{
		{"image/jpeg", "http://x/a.php", ".jpg"},
		{"image/png", "http://x/a", ".png"},
		{"image/webp", "http://x/a.jpg", ".webp"},
		{"image/svg+xml", "http://x/a.svg", ".svg"},
	}
	for _, tc := range cases {
		if got := imageExt(tc.contentType, tc.src); got != tc.want {
			t.Errorf("imageExt(%q, %q) = %q，期望 %q", tc.contentType, tc.src, got, tc.want)
		}
	}
}

// TestValidateImageURLBlocksPrivateHosts 确认默认拒绝回环/内网图片地址。
func TestValidateImageURLBlocksPrivateHosts(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/a.png",
		"http://localhost/a.png",
		"http://10.0.0.1/a.png",
		"http://192.168.1.10/a.png",
		"http://169.254.169.254/latest/meta-data",
	}
	for _, raw := range blocked {
		if err := validateRemoteURL(context.Background(), raw, false); err == nil {
			t.Errorf("%s 应被拒绝", raw)
		}
	}
	if err := validateRemoteURL(context.Background(), "http://127.0.0.1/a.png", true); err != nil {
		t.Errorf("显式允许内网时应放行，实际: %v", err)
	}
}

// TestMD5HexStable 确认缓存键稳定（文件名兼容旧缓存）。
func TestMD5HexStable(t *testing.T) {
	const src = "https://example.com/a.png"
	got := md5Hex(src)
	if len(got) != 32 {
		t.Errorf("md5Hex 长度 = %d，期望 32", len(got))
	}
	if got != md5Hex(src) {
		t.Error("md5Hex 不稳定")
	}
	if empty := md5Hex(""); empty != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("空串 md5 = %q，期望 d41d8cd98f00b204e9800998ecf8427e", empty)
	}
}

// TestCacheWithZeroConcurrencyDoesNotDeadlock 确认并发数为 0 时不会死锁。
//
// make(chan struct{}, 0) 是无缓冲 channel：若直接用作信号量，第一次发送
// 就会永久阻塞。配置校验会拦住非法值，这里守住兜底逻辑。
func TestCacheWithZeroConcurrencyDoesNotDeadlock(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_cache_concurrency": 0}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}
	done := make(chan map[string]string, 1)
	go func() {
		done <- c.Cache(context.Background(), 1, []string{srv.URL + "/a.png"})
	}()

	select {
	case got := <-done:
		if len(got) != 1 {
			t.Errorf("缓存映射 = %v，期望 1 项", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("并发数为 0 时 Cache 卡死")
	}
}

// TestGuardDialBlocksPrivateTargets 确认连接层校验会拦下内网目标。
//
// 这是防重定向/DNS rebinding 的关键：只在请求前校验 URL 不够，
// 真正建连时必须再验一次解析结果。
func TestGuardDialBlocksPrivateTargets(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": false}`)

	transport := guardDial(&http.Transport{})
	blocked := []string{"127.0.0.1:80", "10.0.0.1:443", "192.168.1.10:8080", "0.0.0.0:80", "[::]:80"}
	for _, addr := range blocked {
		if _, err := transport.DialContext(context.Background(), "tcp", addr); err == nil {
			t.Errorf("%s 应被连接层拒绝", addr)
		}
	}
}

// TestGuardDialAllowsWhenConfigured 确认显式放行内网时不拦截。
func TestGuardDialAllowsWhenConfigured(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := rssImageHTTPClient()
	resp, err := client.R().Get(srv.URL)
	if err != nil {
		t.Fatalf("放行内网后不应失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码 = %d", resp.StatusCode)
	}
}

// TestRedirectToPrivateIsBlocked 确认重定向到内网会被逐跳校验拦下。
func TestRedirectToPrivateIsBlocked(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": false}`)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/a.png", http.StatusFound)
	}))
	defer redirector.Close()

	// 初始地址是回环，先绕过初始校验，专门验证重定向那一跳。
	client := rssImageHTTPClient()
	resp, err := client.R().Get(redirector.URL + "/a.png")
	if err == nil {
		resp.Body.Close()
		t.Fatal("重定向到回环地址应失败")
	}
	if !strings.Contains(err.Error(), "内部地址") && !strings.Contains(err.Error(), "重定向") {
		t.Errorf("错误信息应说明地址被拒，实际: %v", err)
	}
}
