package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
)

// testPNGSignature 是足以通过嗅探的最小 PNG 头。
func testPNGSignature() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
}

// TestDirectAvatarSourceBuildURL 确认 URL 结构与 Gravatar 约定一致。
func TestDirectAvatarSourceBuildURL(t *testing.T) {
	src := &directAvatarSource{baseURL: "https://www.gravatar.com/avatar"}

	got := src.buildURL(avatarRequest{
		Hash:   "0bc83cb571cd1c50ba6f3e8a78ef1346",
		Size:   200,
		Style:  "identicon",
		Rating: "g",
	})
	want := "https://www.gravatar.com/avatar/0bc83cb571cd1c50ba6f3e8a78ef1346?d=identicon&r=g&s=200"
	if got != want {
		t.Errorf("buildURL() = %q\n期望 %q", got, want)
	}
}

// TestDirectAvatarSourceTrimsTrailingSlash 确 baseURL 末尾斜杠不会产生双斜杠。
func TestDirectAvatarSourceTrimsTrailingSlash(t *testing.T) {
	src := &directAvatarSource{baseURL: "https://example.com/avatar/"}
	got := src.buildURL(avatarRequest{Hash: "abc", Size: 64, Style: "mp", Rating: "pg"})
	if strings.Contains(got, "//abc") {
		t.Errorf("URL 出现双斜杠: %q", got)
	}
}

// TestExtensionAvatarSourceSendsContract 确认发给 extension 的请求符合契约。
func TestExtensionAvatarSourceSendsContract(t *testing.T) {
	img := testPNGSignature()

	var gotPath, gotQuery, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		_, _ = w.Write(img)
	}))
	defer srv.Close()

	src := newAvatarSource(&config.Config{Gravatar: config.GravatarConfig{
		ExtensionURL:   srv.URL,
		ExtensionToken: "secret-token",
		TimeoutSeconds: 2,
	}})

	payload, notModified, err := src.Fetch(context.Background(), avatarRequest{
		Hash:   "deadbeef",
		Size:   224,
		Style:  "identicon",
		Rating: "g",
	}, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if notModified {
		t.Error("200 响应不应标记 notModified")
	}
	if gotPath != "/avatar/deadbeef" {
		t.Errorf("path = %q，期望 /avatar/deadbeef", gotPath)
	}
	if !strings.Contains(gotQuery, "d=identicon") ||
		!strings.Contains(gotQuery, "r=g") ||
		!strings.Contains(gotQuery, "s=224") {
		t.Errorf("query = %q，缺少 s/d/r 参数", gotQuery)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotAccept, "image/png") {
		t.Errorf("Accept = %q", gotAccept)
	}
	if payload.ContentType != "image/png" {
		t.Errorf("Content-Type = %q", payload.ContentType)
	}
	if payload.ETag != `"abc"` || payload.LastModified == "" {
		t.Errorf("未透传缓存元数据: %+v", payload)
	}
}

// TestExtensionAvatarSourceSendsConditions 确认刷新时携带条件头。
func TestExtensionAvatarSourceSendsConditions(t *testing.T) {
	var gotETag, gotModified string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotETag = r.Header.Get("If-None-Match")
		gotModified = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	src := newAvatarSource(&config.Config{Gravatar: config.GravatarConfig{
		ExtensionURL:   srv.URL,
		TimeoutSeconds: 2,
	}})

	_, notModified, err := src.Fetch(context.Background(), avatarRequest{Hash: "abc", Size: 64, Style: "mp", Rating: "g"},
		&avatarConditions{ETag: `"v1"`, LastModified: "Wed, 21 Oct 2015 07:28:00 GMT"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !notModified {
		t.Error("304 响应应标记 notModified")
	}
	if gotETag != `"v1"` {
		t.Errorf("If-None-Match = %q", gotETag)
	}
	if gotModified == "" {
		t.Error("缺少 If-Modified-Since")
	}
}

// TestExtensionAvatarSourceDoesNotFollowRedirect 确认 extension 地址跳转被视为错误。
//
// extension 地址由运维配置，跳转只可能意味着配置错了或上游被劫持，
// 静默跟随会把凭据带给第三方。
func TestExtensionAvatarSourceDoesNotFollowRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/steal", http.StatusFound)
	}))
	defer srv.Close()

	src := newAvatarSource(&config.Config{Gravatar: config.GravatarConfig{
		ExtensionURL:   srv.URL,
		ExtensionToken: "secret",
		TimeoutSeconds: 2,
	}})

	_, _, err := src.Fetch(context.Background(), avatarRequest{Hash: "abc", Size: 64, Style: "mp", Rating: "g"}, nil)
	if !errors.Is(err, ErrAvatarUpstream) {
		t.Fatalf("错误 = %v，期望 ErrAvatarUpstream", err)
	}
	if !strings.Contains(err.Error(), "302") {
		t.Errorf("错误信息应包含状态码: %v", err)
	}
}

// TestExtensionAvatarSourceMissing 确认 404 被归类为「无头像」而非上游故障。
func TestExtensionAvatarSourceMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := newAvatarSource(&config.Config{Gravatar: config.GravatarConfig{
		ExtensionURL:   srv.URL,
		TimeoutSeconds: 2,
	}})

	_, _, err := src.Fetch(context.Background(), avatarRequest{Hash: "abc", Size: 64, Style: "mp", Rating: "g"}, nil)
	if !errors.Is(err, ErrAvatarNotFound) {
		t.Fatalf("错误 = %v，期望 ErrAvatarNotFound", err)
	}
}

// TestAvatarSourceKeyHidesEndpoint 确认 extension 的 Key 不泄漏内网地址或凭据。
func TestAvatarSourceKeyHidesEndpoint(t *testing.T) {
	src := newAvatarSource(&config.Config{Gravatar: config.GravatarConfig{
		ExtensionURL: "http://10.0.0.5:8090",
	}})
	key := src.Key()
	if !strings.HasPrefix(key, "ext:") {
		t.Fatalf("Key = %q，期望 ext: 前缀", key)
	}
	if strings.Contains(key, "10.0.0.5") {
		t.Errorf("Key 泄漏了 extension 地址: %q", key)
	}
	// 变体长度受数据库列限制（160）。
	if len(key) > 32 {
		t.Errorf("Key 过长: %d", len(key))
	}
}

// TestReadAvatarBodyRejectsOversize 确认超限时失败而不是截断。
func TestReadAvatarBodyRejectsOversize(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", 100))

	if _, err := readAvatarBody(body, 64); !errors.Is(err, ErrAvatarInvalid) {
		t.Fatalf("错误 = %v，期望 ErrAvatarInvalid", err)
	}

	body = strings.NewReader("ok")
	data, err := readAvatarBody(body, 64)
	if err != nil || string(data) != "ok" {
		t.Fatalf("正常响应读取失败: %v / %q", err, data)
	}
}

// TestValidateAvatarImage 覆盖受支持与明确拒绝的格式。
func TestValidateAvatarImage(t *testing.T) {
	png := testPNGSignature()
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01")
	gif := []byte("GIF89a" + strings.Repeat("\x00", 16))
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 16)...)

	cases := []struct {
		name string
		data []byte
		want string
		ok   bool
	}{
		{"png", png, "image/png", true},
		{"jpeg", jpeg, "image/jpeg", true},
		{"gif", gif, "image/gif", true},
		{"webp", webp, "image/webp", true},
		{"html", []byte("<html><body>error</body></html>"), "", false},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), "", false},
		{"empty", nil, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateAvatarImage(tc.data)
			if tc.ok {
				if err != nil {
					t.Fatalf("validateAvatarImage: %v", err)
				}
				if got != tc.want {
					t.Errorf("Content-Type = %q，期望 %q", got, tc.want)
				}
				return
			}
			if !errors.Is(err, ErrAvatarInvalid) {
				t.Errorf("错误 = %v，期望 ErrAvatarInvalid", err)
			}
		})
	}
}

// TestAvatarVariantRoundTrip 确认变体可被解析回原参数（刷新时需要）。
func TestAvatarVariantRoundTrip(t *testing.T) {
	src := &directAvatarSource{baseURL: "https://www.gravatar.com/avatar"}
	variant := avatarVariant(src, 224, "Identicon", "G")

	sourceKey, size, style, rating, ok := parseAvatarVariant(variant)
	if !ok {
		t.Fatalf("变体无法解析: %q", variant)
	}
	if sourceKey != src.Key() || size != 224 || style != "identicon" || rating != "g" {
		t.Errorf("解析结果 = %q/%d/%q/%q", sourceKey, size, style, rating)
	}

	if _, _, _, _, ok := parseAvatarVariant("broken"); ok {
		t.Error("非法变体应返回 ok=false")
	}
	if _, _, _, _, ok := parseAvatarVariant("direct|x|identicon|g"); ok {
		t.Error("尺寸非法时应返回 ok=false")
	}
}

// TestResolveAvatarSize 确认缓存尺寸取自配置并被限制在安全范围内。
func TestResolveAvatarSize(t *testing.T) {
	cases := []struct {
		name     string
		config   *config.Config
		expected int
	}{
		{"配置缺失", nil, gravatarUnspecifiedSize},
		{"配置为零", &config.Config{}, gravatarUnspecifiedSize},
		{"正常值", &config.Config{Gravatar: config.GravatarConfig{DefaultSize: 256}}, 256},
		{"过小被抬到下限", &config.Config{Gravatar: config.GravatarConfig{DefaultSize: 4}}, gravatarMinSize},
		{"过大被压到上限", &config.Config{Gravatar: config.GravatarConfig{DefaultSize: 1024}}, gravatarMaxSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAvatarSize(tc.config); got != tc.expected {
				t.Errorf("resolveAvatarSize() = %d，期望 %d", got, tc.expected)
			}
		})
	}
}

// TestGravatarTimeoutFallback 确认配置缺失时使用 10 秒兜底。
func TestGravatarTimeoutFallback(t *testing.T) {
	if got := gravatarTimeout(nil); got != 10*time.Second {
		t.Errorf("超时 = %v，期望 10s", got)
	}
	cfg := &config.Config{Gravatar: config.GravatarConfig{TimeoutSeconds: 3}}
	if got := gravatarTimeout(cfg); got != 3*time.Second {
		t.Errorf("超时 = %v，期望 3s", got)
	}
}
