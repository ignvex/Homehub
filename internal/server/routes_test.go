package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
