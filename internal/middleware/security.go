// Package middleware 提供 Gin 中间件。
package middleware

import (
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为响应补上基础安全头。
//
// 重点保护上传目录 `/static/uploads/`：该目录内容来自用户上传，
// 若被当作同源 HTML/脚本执行，就能窃取会话 Cookie（会话是 HttpOnly 的，
// 但仍可通过同源 fetch 冒充用户操作）。
//
// 具体措施：
//   - `X-Content-Type-Options: nosniff`：禁止浏览器嗅探 MIME。
//     没有它时，一个 .txt 里放 HTML 也可能被当页面渲染。
//   - SVG 单独加 `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'`：
//     SVG 是 XML，可内嵌 <script>。直接访问该 URL 会执行脚本；
//     禁用脚本与外部资源加载后，它只能作为图片渲染。
//   - `Content-Disposition: inline` 仅对非 SVG 生效，避免 PDF 触发下载。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")

		// 上传目录的响应额外加固
		if strings.HasPrefix(c.Request.URL.Path, "/static/uploads/") {
			switch strings.ToLower(path.Ext(c.Request.URL.Path)) {
			case ".svg":
				h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
				h.Set("X-Frame-Options", "DENY")
			case ".pdf":
				// PDF 由浏览器内建阅读器打开；显式声明避免被当作下载附件。
				h.Set("Content-Disposition", "inline")
			}
		}

		c.Next()
	}
}
