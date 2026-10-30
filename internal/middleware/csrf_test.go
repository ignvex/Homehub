package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newCSRFContext 构造一个带 CSRF 中间件的测试上下文。
func newCSRFContext(method, target string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c, w
}

// TestCSRFAllowsSafeMethods 确认 GET 等安全方法不被拦截。
func TestCSRFAllowsSafeMethods(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		c, w := newCSRFContext(method, "/api/v1/bookmarks", nil)
		CSRFProtect()(c)

		if c.IsAborted() {
			t.Errorf("%s 被错误拦截，状态码 %d", method, w.Code)
		}
	}
}

// TestCSRFBlocksCrossOriginPost 锁住主力防线：
// 浏览器跨站发起的非安全请求一定带 Origin，与本站不符即拒绝。
func TestCSRFBlocksCrossOriginPost(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", map[string]string{
		"Origin": "https://evil.example.com",
	})
	c.Request.Host = "homehub.local"
	CSRFProtect()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("跨站 POST 状态码 = %d，期望 403", w.Code)
	}
	if !c.IsAborted() {
		t.Error("跨站 POST 应被中止")
	}
}

// TestCSRFAllowsSameOriginPost 确认同源 POST 放行。
func TestCSRFAllowsSameOriginPost(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", map[string]string{
		"Origin": "http://homehub.local:8080",
	})
	c.Request.Host = "homehub.local:8080"
	CSRFProtect()(c)

	if c.IsAborted() {
		t.Errorf("同源 POST 被错误拦截，状态码 %d", w.Code)
	}
}

// TestCSRFAllowsSameOriginWithDifferentScheme 确认 scheme 差异不误杀。
//
// 部署常在反向代理后面，TLS 在代理层终止：应用看到的是 http，
// 而浏览器 Origin 是 https。比 scheme 会把所有生产请求拒掉。
func TestCSRFAllowsSameOriginWithDifferentScheme(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", map[string]string{
		"Origin": "https://homehub.local",
	})
	c.Request.Host = "homehub.local"
	CSRFProtect()(c)

	if c.IsAborted() {
		t.Error("反向代理场景（https Origin + http 内部）被误杀")
	}
}

// TestCSRFBlocksNullOrigin 确认 "null" Origin 被拒绝。
//
// 沙箱 iframe、file:// 页面会发出 Origin: null，这是经典的 CSRF 载体。
func TestCSRFBlocksNullOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodDelete, "/api/v1/bookmarks/1", map[string]string{
		"Origin": "null",
	})
	CSRFProtect()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("Origin: null 状态码 = %d，期望 403", w.Code)
	}
}

// TestCSRFAllowsNonBrowserClient 确认没有 Origin/Referer 的请求放行。
//
// curl、脚本、服务端到服务端调用不会带这两个头，它们也不具备
// 浏览器“自动附带 cookie”的特性，因此不存在 CSRF 风险。
func TestCSRFAllowsNonBrowserClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", nil)
	CSRFProtect()(c)

	if c.IsAborted() {
		t.Errorf("无 Origin 的客户端被误杀，状态码 %d", w.Code)
	}
}

// TestCSRFValidatesDoubleToken 确认回传的 token 与 cookie 不符时拒绝。
//
// 这道防线挡的是“Origin 被伪造”的场景：非浏览器客户端可以随意写
// Origin，但它拿不到本站 cookie 里的 token。
func TestCSRFValidatesDoubleToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 带了错误 token
	c, w := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", map[string]string{
		CSRFHeaderName: "wrong-token",
	})
	c.Request.Host = "homehub.local"
	c.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "correct-token"})
	CSRFProtect()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("token 不符状态码 = %d，期望 403", w.Code)
	}

	// 带了正确 token
	c2, w2 := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", map[string]string{
		CSRFHeaderName: "correct-token",
	})
	c2.Request.Host = "homehub.local"
	c2.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "correct-token"})
	CSRFProtect()(c2)

	if c2.IsAborted() {
		t.Errorf("正确 token 被拦截，状态码 %d", w2.Code)
	}
}

// TestCSRFRejectsTokenWithoutCookie 确认没有 cookie 时回传 token 也拒绝。
//
// 攻击者可以自己造一个 token 放进请求头，但没有办法给本站设 cookie。
func TestCSRFRejectsTokenWithoutCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodPost, "/api/v1/bookmarks", map[string]string{
		CSRFHeaderName: "self-made-token",
	})
	CSRFProtect()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("无 cookie 时状态码 = %d，期望 403", w.Code)
	}
}

// TestCSRFIssuesTokenOnSafeRequest 确认 GET 会下发 token cookie。
func TestCSRFIssuesTokenOnSafeRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodGet, "/api/v1/bookmarks", nil)
	CSRFProtect()(c)

	cookies := w.Result().Cookies()
	var found *http.Cookie
	for _, ck := range cookies {
		if ck.Name == csrfCookieName {
			found = ck
			break
		}
	}
	if found == nil {
		t.Fatal("GET 未下发 CSRF cookie")
	}
	if found.Value == "" {
		t.Error("CSRF token 为空")
	}
	if found.HttpOnly {
		t.Error("CSRF cookie 不能是 HttpOnly —— 前端需要读取它做双提交")
	}
	if got := w.Header().Get(CSRFHeaderName); got == "" {
		t.Error("响应头未回传 CSRF token")
	}
}

// TestCSRFDoesNotReissueToken 确认已有 token 时不重复 Set-Cookie。
func TestCSRFDoesNotReissueToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, w := newCSRFContext(http.MethodGet, "/api/v1/bookmarks", nil)
	c.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "existing"})
	CSRFProtect()(c)

	for _, ck := range w.Result().Cookies() {
		if ck.Name == csrfCookieName {
			t.Error("已有 token 时不应重复下发 cookie")
		}
	}
}
