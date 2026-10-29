package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// runSecurity 让一个请求穿过 SecurityHeaders 中间件并返回响应头。
func runSecurity(path string) http.Header {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)

	SecurityHeaders()(c)
	return w.Header()
}

// TestSecurityHeadersNosniffOnEverything 确认所有响应都带 nosniff。
func TestSecurityHeadersNosniffOnEverything(t *testing.T) {
	for _, p := range []string{"/", "/api/v1/health", "/static/uploads/a.png"} {
		h := runSecurity(p)
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s 的 X-Content-Type-Options = %q，期望 nosniff", p, got)
		}
	}
}

// TestSecurityHeadersSvgGetsCSP 确认 SVG 被限制为不可执行。
//
// SVG 是 XML，可内嵌 <script>。用户上传的 SVG 若被直接访问会执行脚本，
// 必须用 CSP 禁用脚本与外部资源加载。
func TestSecurityHeadersSvgGetsCSP(t *testing.T) {
	h := runSecurity("/static/uploads/rss_img/logo.svg")

	csp := h.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("SVG 响应缺少 Content-Security-Policy")
	}
	if !contains(csp, "default-src 'none'") {
		t.Errorf("CSP 未禁用默认来源: %q", csp)
	}
	if h.Get("X-Frame-Options") != "DENY" {
		t.Error("SVG 响应应设置 X-Frame-Options: DENY")
	}
}

// TestSecurityHeadersSvgUpperCase 确认大写扩展名同样被识别。
func TestSecurityHeadersSvgUpperCase(t *testing.T) {
	h := runSecurity("/static/uploads/LOGO.SVG")
	if h.Get("Content-Security-Policy") == "" {
		t.Error("大写 .SVG 也应加 CSP")
	}
}

// TestSecurityHeadersPdfInline 确认 PDF 以内联方式打开。
func TestSecurityHeadersPdfInline(t *testing.T) {
	h := runSecurity("/static/uploads/doc.pdf")
	if got := h.Get("Content-Disposition"); got != "inline" {
		t.Errorf("PDF 的 Content-Disposition = %q，期望 inline", got)
	}
}

// TestSecurityHeadersSvgOutsideUploads 确认只加固上传目录。
//
// 项目自带的 favicon 等静态资源不应被 CSP 限制。
func TestSecurityHeadersSvgOutsideUploads(t *testing.T) {
	h := runSecurity("/static/favicon.svg")
	if h.Get("Content-Security-Policy") != "" {
		t.Error("非上传目录的 SVG 不应被 CSP 限制")
	}
	// nosniff 仍然要加
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("非上传目录也应带 nosniff")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
