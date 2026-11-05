package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/handler"
	"github.com/ignvex/HomeHub/internal/service"
)

// newTestEngine 构建与生产一致的路由表（不含静态资源与 swagger）。
func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerRoutes(r)
	registerSPAFallback(r)
	return r
}

// TestRoutesHaveNoBilibili 确认路由表中不再存在 Bilibili 爬虫相关端点。
//
// 爬虫模块已拆分为独立项目，本仓库只保留数据平台自身功能。
func TestRoutesHaveNoBilibili(t *testing.T) {
	r := newTestEngine()

	for _, route := range r.Routes() {
		if strings.Contains(strings.ToLower(route.Path), "bili") {
			t.Errorf("残留 Bilibili 路由: %s %s", route.Method, route.Path)
		}
	}

	// 已移除的端点必须返回 404，而不是落到 SPA fallback 返回 HTML。
	for _, path := range []string{
		"/api/v1/bilibili/stats",
		"/api/v1/bilibili/videos",
		"/api/v1/bilibili/crawlers",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", path, w.Code)
		}
	}
}

// TestCoreRoutesRegistered 确认核心业务端点仍然注册完整。
//
// 与上一条配合，确保移除爬虫时没有误删其他路由。
func TestCoreRoutesRegistered(t *testing.T) {
	r := newTestEngine()

	registered := make(map[string]bool, len(r.Routes()))
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	want := []string{
		"GET /api/v1/health",
		"POST /api/v1/auth/login",
		"GET /api/v1/auth/status",
		"GET /api/v1/bookmarks",
		"GET /api/v1/bookmark-groups",
		"GET /api/v1/rss/feeds",
		"GET /api/v1/rss/entries",
		"GET /api/v1/rss/schedule",
		"GET /api/v1/todos",
		"GET /api/v1/media-assets",
		"GET /api/v1/balances",
		"GET /api/v1/pastes",
		"GET /api/v1/search",
		"GET /api/v1/admin/logs",
		"GET /api/v1/admin/logs/stream",
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("缺少路由: %s", route)
		}
	}
}

// TestHealthEndpoint 确认健康检查在无数据库时仍返回 200 与结构化状态。
func TestHealthEndpoint(t *testing.T) {
	r := newTestEngine()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health 状态码 = %d，期望 200", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("health 响应不是 JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("health status = %v，期望 ok", body["status"])
	}
	if _, ok := body["db"]; !ok {
		t.Fatal("health 响应缺少 db 字段")
	}
	// 仪表盘的“运行时长”直接读 uptime_seconds，缺字段会导致界面恒为 “—”。
	if _, ok := body["uptime_seconds"]; !ok {
		t.Fatal("health 响应缺少 uptime_seconds 字段")
	}
	if ts, ok := body["timestamp"].(string); !ok || ts == "" {
		t.Fatalf("health 响应缺少 timestamp 或为空: %v", body["timestamp"])
	}
}

// TestExtensionHubRoutesRequireAuth 确认整个 Extension 对接组都在认证链后面。
//
// 这一组会向外发起签名请求，也能签发登录凭证。漏挂 AuthRequired 的后果
// 比普通内容端点更重，所以每个端点都要单独验证。
func TestExtensionHubRoutesRequireAuth(t *testing.T) {
	r := newTestEngine()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/extension/status"},
		{http.MethodGet, "/api/v1/extension/subscriptions"},
		{http.MethodPost, "/api/v1/extension/subscriptions"},
		{http.MethodPost, "/api/v1/extension/subscriptions/sync"},
		{http.MethodDelete, "/api/v1/extension/subscriptions/7"},
		{http.MethodGet, "/api/v1/extension/keys"},
		{http.MethodPost, "/api/v1/extension/keys"},
		{http.MethodDelete, "/api/v1/extension/keys/01JC8Z"},
		{http.MethodGet, "/api/v1/extension/key"},
		{http.MethodPut, "/api/v1/extension/key"},
		{http.MethodDelete, "/api/v1/extension/key"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 未登录时状态码 = %d，期望 401", tc.method, tc.path, w.Code)
		}
	}
}

// TestConfigRoutesRequireAdmin 确认配置接口只对管理员开放。
//
// /config 可改写数据库连接与 session_secret，普通用户若能调用，
// 等于可以篡改会话签名、劫持全站登录态。前端把设置页标记为 admin，
// 后端必须同样生效——否则只是"藏起来"而非真正的访问控制。
func TestConfigRoutesRequireAdmin(t *testing.T) {
	r := newTestEngine()

	// 未登录：应被认证中间件拦下（401）
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/config"},
		{http.MethodPut, "/api/v1/config"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 未登录时状态码 = %d，期望 401", tc.method, tc.path, w.Code)
		}
	}
}

// TestAdminGroupRequiresAdmin 确认 /admin 组同样受管理员校验保护。
func TestAdminGroupRequiresAdmin(t *testing.T) {
	r := newTestEngine()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/admin/users 未登录时状态码 = %d，期望 401", w.Code)
	}
}

// TestConfigGroupMountsAdminMiddleware 确认 /config 路由组挂载了管理员校验。
//
// 上一条测试只能证明"未登录被 401 拦下"（即 AuthRequired 在链上），
// 无法区分是否还挂了 AuthAdminRequired —— 那需要真实数据库会话。
// 这里直接对路由注册源码做结构断言，守住"有人删掉这行 Use"的回归。
func TestConfigGroupMountsAdminMiddleware(t *testing.T) {
	const windowSize = 200

	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("读取 routes.go 失败: %v", err)
	}

	marker := `Group("/config")`
	i := strings.Index(string(src), marker)
	if i == -1 {
		t.Fatalf("未找到 %s 路由组定义", marker)
	}

	end := i + windowSize
	if end > len(src) {
		end = len(src)
	}
	window := string(src[i:end])

	if !strings.Contains(window, "AuthAdminRequired") {
		t.Errorf("/config 路由组未挂载 AuthAdminRequired，普通用户将能改写数据库配置与 session_secret\n"+
			"注册片段:\n%s", window)
	}
}

// TestStaticAssetsCachePolicy 锁住静态资源的两档缓存策略。
//
// 背景：index.html 与构建产物原本都没有 Cache-Control，靠浏览器启发式
// 缓存。结果改完前端刷新页面仍是旧代码；更糟的情况是旧 JS 已被下一次
// 构建删除、而浏览器还拿着一份指向它的 index.html，页面直接白屏。
//
// 这里的断言防的就是这两件事：
//   - index.html 必须每次验证（no-cache），不能长期缓存；
//   - /assets/ 下的哈希命名产物必须 immutable，不能每次重新下载。
func TestStaticAssetsCachePolicy(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/assets/index-D3emB2dI.js", assetsCacheControl},
		{"/assets/index-CY7hc-XF.css", assetsCacheControl},
		{"/favicon.svg", htmlCacheControl},
		{"//", htmlCacheControl},
	}
	for _, c := range cases {
		if got := staticCacheControl(c.path); got != c.want {
			t.Errorf("staticCacheControl(%q) = %q，期望 %q", c.path, got, c.want)
		}
	}
}

// TestIndexHTMLIsNotLongCached 确认 SPA 入口真的带上了 no-cache 头。
//
// 只测纯函数不够：头部是否真的写进了响应，取决于 serveSPA 有没有调用
// 它。这条用真实请求验证，避免「函数改了但没接上」。
func TestIndexHTMLIsNotLongCached(t *testing.T) {
	// serveSPA 用的是相对工作目录的 "web/dist/index.html"，而 go test
	// 在包目录下运行。切到仓库根，否则这里会因为找不到文件而 404，
	// 掩盖掉真正要验证的头部逻辑。
	t.Chdir("../..")

	// 没有构建产物时跳过，避免干净检出上假失败。
	if _, err := os.Stat("web/dist/index.html"); err != nil {
		t.Skip("web/dist 不存在，跳过静态资源断言")
	}

	r := newTestEngine()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET / 状态码 = %d，期望 200", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != htmlCacheControl {
		t.Errorf("GET / 的 Cache-Control = %q，期望 %q", got, htmlCacheControl)
	}
}

// TestSearchRequiresAuth 确认联合搜索不对匿名访客开放。
//
// 搜索会命中 RSS 条目正文与书签，RSS 读接口本身要求登录；
// 搜索若挂在公开组里，就成了绕开 /rss 认证读取订阅内容的旁路。
func TestSearchRequiresAuth(t *testing.T) {
	r := newTestEngine()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/search 未登录时状态码 = %d，期望 401", w.Code)
	}
}

// TestPasteCreateIsRateLimitedPerIP 确认匿名粘贴接口真的挂了限流。
//
// 限流器本身在 middleware 包里有单测；这里防的是「中间件写好了
// 但没挂到路由上」。超出每分钟 10 次后必须 429，且换一个 IP 不受影响。
func TestPasteCreateIsRateLimitedPerIP(t *testing.T) {
	handler.Init(&service.Services{})
	t.Cleanup(func() { handler.Init(nil) })
	r := newTestEngine()

	post := func(ip string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip + ":40000"
		r.ServeHTTP(w, req)
		return w.Code
	}

	const ip = "198.51.100.77"
	for i := range 10 {
		if code := post(ip); code == http.StatusTooManyRequests {
			t.Fatalf("第 %d 次就被限流", i+1)
		}
	}
	if code := post(ip); code != http.StatusTooManyRequests {
		t.Fatalf("第 11 次状态码 = %d，期望 429", code)
	}
	if code := post("198.51.100.78"); code == http.StatusTooManyRequests {
		t.Fatal("其他 IP 不应被连带限流")
	}
}

// TestRSSReadStateRoutesAreLoginOnly 锁住阅读状态端点的权限门槛。
//
// 已读/星标是个人数据，5 级只读用户也必须能用；挂到 contentWrite（3 级）
// 组下会让只读用户永远无法标记已读。这里断言它们注册在 rssRead 组上，
// 并且未登录时仍被 401 拦下。
func TestRSSReadStateRoutesAreLoginOnly(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("读取 routes.go 失败: %v", err)
	}
	for _, line := range []string{
		`rssRead.PUT("/entries/:id/read"`,
		`rssRead.DELETE("/entries/:id/read"`,
		`rssRead.PUT("/entries/:id/star"`,
		`rssRead.DELETE("/entries/:id/star"`,
		`rssRead.POST("/entries/mark-read"`,
		`rssRead.GET("/unread-counts"`,
	} {
		if !strings.Contains(string(src), line) {
			t.Errorf("阅读状态路由应注册在登录即可的 rssRead 组: 缺少 %s", line)
		}
	}

	r := newTestEngine()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/rss/entries/1/read"},
		{http.MethodDelete, "/api/v1/rss/entries/1/read"},
		{http.MethodPut, "/api/v1/rss/entries/1/star"},
		{http.MethodDelete, "/api/v1/rss/entries/1/star"},
		{http.MethodPost, "/api/v1/rss/entries/mark-read"},
		{http.MethodGet, "/api/v1/rss/unread-counts"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 未登录时状态码 = %d，期望 401", tc.method, tc.path, w.Code)
		}
	}
}

// TestImportExportRoutesRequireAuth 确认导入导出端点都在认证之后。
//
// 书签列表是公开读的；整库导出若误挂到 publicAPI，就成了匿名下载全部书签。
func TestImportExportRoutesRequireAuth(t *testing.T) {
	r := newTestEngine()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/bookmarks/export"},
		{http.MethodPost, "/api/v1/bookmarks/import"},
		{http.MethodGet, "/api/v1/rss/opml"},
		{http.MethodPost, "/api/v1/rss/opml"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 未登录时状态码 = %d，期望 401", tc.method, tc.path, w.Code)
		}
	}
}

// TestTodoWriteGateIsMember 锁住待办写路由的门槛：4 级（member）。
//
// 门槛放宽到 4 级的前提是 service 层按归属校验；若有人把它改回
// contentWrite（3 级），4 级用户的「我的待办」会整体 403，而改成
// authRequired 不带等级则会让 5 级只读用户也能写。
func TestTodoWriteGateIsMember(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("读取 routes.go 失败: %v", err)
	}
	marker := `todoWrite := authRequired.Group("/todos")`
	i := strings.Index(string(src), marker)
	if i == -1 {
		t.Fatalf("未找到 %s", marker)
	}
	window := string(src[i : i+200])
	if !strings.Contains(window, "AuthLevelRequired(model.LevelMember)") {
		t.Errorf("待办写路由组应挂 AuthLevelRequired(model.LevelMember):\n%s", window)
	}

	r := newTestEngine()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/users/directory", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/users/directory 未登录时状态码 = %d，期望 401", w.Code)
	}
}
