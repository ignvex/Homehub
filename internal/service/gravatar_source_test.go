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
		// HTML 走专门的错误（见 TestValidateAvatarImageFlagsHTMLEndpoint），
		// 这里只断言它被拒绝，不断言具体分类。
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
			if !errors.Is(err, ErrAvatarInvalid) && !errors.Is(err, ErrAvatarEndpointHTML) {
				t.Errorf("错误 = %v，期望 ErrAvatarInvalid 或 ErrAvatarEndpointHTML", err)
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

// TestValidateAvatarImageFlagsHTMLEndpoint 确认 HTML 响应归到专门的错误。
//
// 配错 extension_url 指向一个没有 /avatar 的服务时，对方会用 200
// 返回首页 HTML。「上游返回的头像格式不合法」会把人送去查图片格式，
// 而真正的修法是改地址——两种情况必须能分开。
func TestValidateAvatarImageFlagsHTMLEndpoint(t *testing.T) {
	html := []byte("<!doctype html><html><head><title>Homehub</title></head><body></body></html>")

	_, err := validateAvatarImage(html)
	if err == nil {
		t.Fatal("HTML 必须被拒绝")
	}
	if !errors.Is(err, ErrAvatarEndpointHTML) {
		t.Fatalf("HTML 应归入 ErrAvatarEndpointHTML，实际 %v", err)
	}
	if errors.Is(err, ErrAvatarInvalid) {
		t.Fatal("不应同时算作「格式不合法」，两者修法不同")
	}
}

// TestValidateAvatarImageKeepsInvalidForNonHTML 确认非 HTML 的坏数据
// 仍走原来的分类，不被新分支抢走。
func TestValidateAvatarImageKeepsInvalidForNonHTML(t *testing.T) {
	_, err := validateAvatarImage([]byte("\x00\x01\x02not-an-image"))
	if err == nil {
		t.Fatal("无法识别的数据必须被拒绝")
	}
	if errors.Is(err, ErrAvatarEndpointHTML) {
		t.Fatal("非 HTML 不应归入端点配置错误")
	}
	if !errors.Is(err, ErrAvatarInvalid) {
		t.Fatalf("应归入 ErrAvatarInvalid，实际 %v", err)
	}
}

// TestDirectAvatarSourceHintsProxyOnTimeout 确认超时且未配代理时带上提示。
//
// 这个组合很容易踩：部署在必须经代理出网的环境里，gravatar.proxy_names
// 忘了填。原始错误只有 "context deadline exceeded"，看不出是网络不通还是
// 没配代理，排查会先怀疑 Gravatar 挂了。提示要能指回配置项。
func TestDirectAvatarSourceHintsProxyOnTimeout(t *testing.T) {
	// 能连上但永不响应，制造一个确定性的超时。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()

	src := &directAvatarSource{
		client:          &http.Client{Timeout: 100 * time.Millisecond},
		baseURL:         upstream.URL,
		proxyConfigured: false,
	}

	_, _, err := src.Fetch(context.Background(), avatarRequest{Hash: "abc", Style: "identicon", Rating: "g", Size: 256}, nil)
	if err == nil {
		t.Fatal("期望超时错误")
	}
	if !errors.Is(err, ErrAvatarUpstream) {
		t.Errorf("错误应可识别为上游故障，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "gravatar.proxy_names") {
		t.Errorf("提示应指向配置项 gravatar.proxy_names，实际: %v", err)
	}
}

// TestDirectAvatarSourceNoHintWhenProxyConfigured 确认配了代理时不加这条提示。
//
// 配了代理还超时，原因在别处（代理本身不通、目标站点封锁）。
// 这时继续提示"去配代理"只会把人引向错误方向。
func TestDirectAvatarSourceNoHintWhenProxyConfigured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()

	src := &directAvatarSource{
		client:          &http.Client{Timeout: 100 * time.Millisecond},
		baseURL:         upstream.URL,
		proxyConfigured: true,
	}

	_, _, err := src.Fetch(context.Background(), avatarRequest{Hash: "abc", Style: "identicon", Rating: "g", Size: 256}, nil)
	if err == nil {
		t.Fatal("期望超时错误")
	}
	if strings.Contains(err.Error(), "gravatar.proxy_names") {
		t.Errorf("已配代理时不应提示去配代理，实际: %v", err)
	}
}
