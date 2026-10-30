package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// CSRFHeaderName 是客户端回传 CSRF token 的请求头。
const CSRFHeaderName = "X-CSRF-Token"

// csrfCookieName 存 CSRF token 的 cookie 名。
//
// 它**不能**是 HttpOnly：双提交策略要求 JS 能读到它并放进请求头。
// 这不削弱安全性——CSRF token 不是凭据，泄露它本身没用，
// 攻击者需要同时伪造“cookie 里有这个值”和“请求头里有这个值”，
// 而跨站请求既读不到本站 cookie、也无法给本站设 cookie。
const csrfCookieName = "homehub_csrf"

// csrfTokenLen 是 token 的原始字节数（hex 后翻倍）。
const csrfTokenLen = 32

// safeMethods 不改变状态，不需要 CSRF 校验。
var safeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// CSRFProtect 返回 CSRF 防护中间件。
//
// 采用两道独立防线，任何一道命中即拒绝：
//
//  1. **Origin / Referer 校验**（主力）。浏览器对跨站发起的非安全请求
//     一定会带上 Origin，因此 Origin 与本站 Host 不符就等于跨站提交。
//     这条不需要前端配合，对现有客户端零改动。
//
//  2. **双提交 Cookie**（加强）。服务端下发一个随机 token 到非 HttpOnly
//     cookie；客户端若在 `X-CSRF-Token` 头里带回同样的值，就再比对一次。
//     这一步挡的是“Origin 被伪造”的场景（非浏览器客户端可以随意写 Origin）。
//
// 取舍说明：**两者都缺失时放行**。原因是 curl / 脚本 / 服务端到服务端的
// 调用本来就不会带 Origin，它们也不具备浏览器“自动附带 cookie”的特性，
// 因此不存在 CSRF 风险。如果把这种情况也拒掉，会把所有非浏览器客户端
// 一并打死，而安全上没有任何收益。
func CSRFProtect() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 安全方法：只负责确保客户端手里有 token。
		if safeMethods[c.Request.Method] {
			ensureCSRFToken(c)
			c.Next()
			return
		}

		// 第一道：Origin / Referer 必须与本站同源。
		if !sameOriginPresent(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 10004, "message": "跨站请求被拒绝",
			})
			return
		}

		// 第二道：如果客户端回传了 token，必须与 cookie 一致。
		if sent := c.GetHeader(CSRFHeaderName); sent != "" {
			stored, err := c.Cookie(csrfCookieName)
			if err != nil || stored == "" ||
				subtle.ConstantTimeCompare([]byte(sent), []byte(stored)) != 1 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"code": 10004, "message": "CSRF 令牌校验失败",
				})
				return
			}
		}

		c.Next()
	}
}

// ensureCSRFToken 在客户端还没有 token 时下发一个。
func ensureCSRFToken(c *gin.Context) {
	if existing, err := c.Cookie(csrfCookieName); err == nil && existing != "" {
		// 已有序号无需重发，避免每次请求都写 Set-Cookie。
		c.Header(CSRFHeaderName, existing)
		return
	}

	buf := make([]byte, csrfTokenLen)
	if _, err := rand.Read(buf); err != nil {
		// 熵源不可用时不发 token，让双提交这道防线自然失效；
		// Origin 校验仍然生效，不会因此全体放行。
		return
	}
	token := hex.EncodeToString(buf)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(csrfCookieName, token, 0, "/", "", false, false)
	c.Header(CSRFHeaderName, token)
}

// sameOriginPresent 判断请求是否“没有跨站迹象”。
//
// 返回 false 表示明确跨站；返回 true 表示同源、或根本没带这两个头
// （非浏览器客户端）。
func sameOriginPresent(r *http.Request) bool {
	// Origin 优先：它是浏览器强制加上的，且脚本无法伪造。
	if origin := r.Header.Get("Origin"); origin != "" {
		return originMatchesHost(origin, r.Host)
	}
	// 没有 Origin 时看 Referer。
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		return originMatchesHost(u.Scheme+"://"+u.Host, r.Host)
	}
	// 两个都没有：不是浏览器发起的，CSRF 不适用。
	return true
}

// originMatchesHost 比较 Origin 与请求的 Host。
//
// 只比 host:port，不比 scheme：部署常在反向代理后面，TLS 在代理层终止，
// 应用看到的 scheme 与客户端不同（http vs https），比 scheme 会误杀。
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// "null" Origin（沙箱 iframe、file:// 等）一律拒绝。
	if u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
