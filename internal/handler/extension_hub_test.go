package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// writeTestSigner 生成一把临时 Ed25519 私钥，返回路径与公钥。
// writeTestSigner 生成一把 PKCS#8 PEM 私钥，返回 PEM 文本与公钥。
//
// 私钥不再落文件：唯一来源是 config.json 的 extension.private_key_pem。
func writeTestSigner(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return string(block), pub
}

// loadExtensionHubConfig 写入并加载一份带 extension 段的最小配置。
func loadExtensionHubConfig(t *testing.T, enabled bool, baseURL, pemText string) {
	t.Helper()

	body := fmt.Sprintf(`{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "%s"},
		"log": {"level": "error", "format": "json"},
		"extension": {"enabled": %t, "base_url": %q, "private_key_pem": %s, "timeout_seconds": 5,
		              "features": {"hub": true}}
	}`, strings.Repeat("a", 32), enabled, baseURL, strconv.Quote(pemText))

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
}

// TestExtensionHubUpstream401Becomes502 锁住上游状态码不透传的约定。
//
// Extension 的 401 表示「宿主签名不对」，与 HomeHub 的会话无关。
// 如果原样透传，前端 api.ts 会把用户踢回登录页——把一次部署配置错误
// 演成「你掉登录了」，是最糟糕的表现。
func TestExtensionHubUpstream401Becomes502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "unauthenticated", "message": "签名校验失败"},
		})
	}))
	defer upstream.Close()

	pemText, _ := writeTestSigner(t)
	loadExtensionHubConfig(t, true, upstream.URL+"/api/v1", pemText)

	// db 留空：401 在访问本地订阅表之前就返回，不会走到查询。
	svc = &service.Services{ExtensionHub: service.NewExtensionHubService(nil)}

	r := gin.New()
	r.GET("/api/v1/extension/subscriptions", ExtensionHubListSubscriptions)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/extension/subscriptions", nil))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502（不能透传上游的 401）", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "70002") {
		t.Fatalf("响应应带 code 70002，实际 %s", body)
	}
	if !strings.Contains(body, "签名校验失败") {
		t.Fatalf("响应应保留上游的可读说明，实际 %s", body)
	}
}

// TestExtensionHubUpstream404Becomes502 确认上游的 404 不被当成 HomeHub 的资源缺失。
//
// 透传 404 会让前端以为「这条订阅在 HomeHub 里不存在」，进而把它
// 从本地状态里移除——一次上游误判就变成数据丢失。
func TestExtensionHubUpstream404Becomes502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "not_found", "message": "RSS 模块未启用"},
		})
	}))
	defer upstream.Close()

	pemText, _ := writeTestSigner(t)
	loadExtensionHubConfig(t, true, upstream.URL+"/api/v1", pemText)
	svc = &service.Services{ExtensionHub: service.NewExtensionHubService(nil)}

	r := gin.New()
	r.GET("/api/v1/extension/subscriptions", ExtensionHubListSubscriptions)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/extension/subscriptions", nil))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", w.Code)
	}
}

// TestExtensionHubDisabledReturns503WithOwnCode 确认前端能区分「没开」与「连不上」。
//
// 两者都用 502 的话，界面只能显示「同步失败」，用户没法判断该去改配置
// 还是该去查网络。70001 让前端可以收起入口而不是弹错误。
func TestExtensionHubDisabledReturns503WithOwnCode(t *testing.T) {
	loadExtensionHubConfig(t, false, "", "")
	svc = &service.Services{ExtensionHub: service.NewExtensionHubService(nil)}

	r := gin.New()
	r.GET("/api/v1/extension/subscriptions", ExtensionHubListSubscriptions)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/extension/subscriptions", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "70001") {
		t.Fatalf("响应应带 code 70001，实际 %s", w.Body.String())
	}
}

// TestExtensionHubStatusExposesPublicKeyForVerification
// 确认状态接口会返回宿主公钥：部署时要在 Extension 侧比对这把钥匙，
// 没有它就只能靠“签名一直失败”去猜，排障成本差一个数量级。
func TestExtensionHubStatusExposesPublicKeyForVerification(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer upstream.Close()

	pemText, pub := writeTestSigner(t)
	loadExtensionHubConfig(t, true, upstream.URL+"/api/v1", pemText)
	svc = &service.Services{ExtensionHub: service.NewExtensionHubService(nil)}

	r := gin.New()
	r.GET("/api/v1/extension/status", ExtensionHubStatus)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/extension/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Enabled       bool   `json:"enabled"`
			Configured    bool   `json:"configured"`
			PublicKey     string `json:"public_key"`
			Reachable     bool   `json:"reachable"`
			Authenticated bool   `json:"authenticated"`
			RSSAvailable  bool   `json:"rss_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if !resp.Data.Enabled || !resp.Data.Configured {
		t.Fatalf("应为已启用且配置齐全，实际 %+v", resp.Data)
	}
	if resp.Data.PublicKey == "" {
		t.Fatal("public_key 不能为空")
	}
	if !resp.Data.Reachable || !resp.Data.Authenticated || !resp.Data.RSSAvailable {
		t.Fatalf("全部就绪时三项都应为 true，实际 %+v", resp.Data)
	}
	_ = pub
}

// TestExtensionHubIssueKeyRejectsBlankName 确认纯空白名称在本地就被拦下。
//
// binding:"required" 只判空字符串，空格能通过。若不在这里拦住，
// 请求会打到一个正常工作的 Extension 并换回 400 invalid_request，
// 用户看到的是“上游拒绝”，方向完全错了。
func TestExtensionHubIssueKeyRejectsBlankName(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "x", "name": "x", "key": "K"})
	}))
	defer upstream.Close()

	pemText, _ := writeTestSigner(t)
	loadExtensionHubConfig(t, true, upstream.URL+"/api/v1", pemText)
	svc = &service.Services{ExtensionHub: service.NewExtensionHubService(nil)}

	r := gin.New()
	r.POST("/api/v1/extension/keys", ExtensionHubIssueKey)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/keys", strings.NewReader(`{"name":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400，body=%s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Fatalf("空白名称不应发出请求，实际发了 %d 次", calls)
	}
}

// TestExtensionHubStorageFailureIsNotReportedAsUpstream 锁住本地故障与上游
// 故障的区分。
//
// 读取本地订阅表失败却报「上游 Extension 不可达」，会把人送到另一台机器上
// 查一个根本没问题的服务。两者必须给出不同的状态码与错误码。
func TestExtensionHubStorageFailureIsNotReportedAsUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer upstream.Close()

	pemText, _ := writeTestSigner(t)
	loadExtensionHubConfig(t, true, upstream.URL+"/api/v1", pemText)

	// 用一个已关闭的 DB 连接制造查询失败。
	db, _, cleanup := testutil.NewMockDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接失败: %v", err)
	}
	cleanup()
	_ = sqlDB.Close()

	svc = &service.Services{ExtensionHub: service.NewExtensionHubService(db)}

	r := gin.New()
	r.POST("/api/v1/extension/subscriptions", ExtensionHubPushSubscription)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/subscriptions",
		strings.NewReader(`{"feed_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusBadGateway {
		t.Fatalf("本地数据库故障不能报 502 上游错误，body=%s", w.Body.String())
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500，body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "70002") {
		t.Fatalf("不能带 70002（上游错误码），body=%s", w.Body.String())
	}
}
