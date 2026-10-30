package service

import (
	"context"
	"strings"
	"testing"
)

// sanitizePlain 在关闭图片缓存的前提下清洗内容，专注标签/属性处理。
func sanitizePlain(t *testing.T, content string) string {
	t.Helper()
	loadConfigWith(t, `,"rss": {"image_cache_enabled": false}`)

	s := &RSSService{}
	return s.sanitizeContent(context.Background(), 1, content)
}

// TestSanitizeDropsScriptAndIframe 确认危险标签连同内容一起删除。
//
// 正则实现删除的是标签本身，脚本文本会作为可见文字留下；这里要求整棵
// 子树消失。
func TestSanitizeDropsScriptAndIframe(t *testing.T) {
	got := sanitizePlain(t, `<p>keep</p><script>alert("xss")</script><iframe src="http://evil.test">fallback</iframe>`)

	if !strings.Contains(got, "keep") {
		t.Errorf("正文不应被丢弃: %s", got)
	}
	if strings.Contains(got, "alert") {
		t.Errorf("脚本文本应被删除: %s", got)
	}
	if strings.Contains(got, "iframe") || strings.Contains(got, "fallback") {
		t.Errorf("iframe 及其内容应被删除: %s", got)
	}
}

// TestSanitizeStripsEventHandlers 确认 on* 事件属性被移除。
//
// 这是正则实现的主要漏洞：`<img src=x onerror=...>` 里的 onerror
// 不在删除规则内。
func TestSanitizeStripsEventHandlers(t *testing.T) {
	got := sanitizePlain(t, `<img src="http://example.com/a.png" onerror="alert(1)" onload="x()"><p onclick="y()">text</p>`)

	if strings.Contains(got, "onerror") || strings.Contains(got, "onload") || strings.Contains(got, "onclick") {
		t.Errorf("事件属性应被移除: %s", got)
	}
	if !strings.Contains(got, "text") {
		t.Errorf("正文不应被丢弃: %s", got)
	}
}

// TestSanitizeRemovesJavascriptURL 确认 javascript:/vbscript: 链接被移除。
func TestSanitizeRemovesJavascriptURL(t *testing.T) {
	got := sanitizePlain(t, `<a href="javascript:alert(1)">a</a><a href="vbscript:x">b</a><a href="JaVaScRiPt:alert(2)">c</a>`)

	if strings.Contains(strings.ToLower(got), "javascript:") || strings.Contains(strings.ToLower(got), "vbscript:") {
		t.Errorf("危险协议应被移除: %s", got)
	}
	// 文字保留，只是没有链接
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") || !strings.Contains(got, "c") {
		t.Errorf("链接文字应保留: %s", got)
	}
}

// TestSanitizeKeepsSafeLinks 确认 http/https/mailto 与相对链接保留。
func TestSanitizeKeepsSafeLinks(t *testing.T) {
	content := `<a href="https://example.com/x">https</a>` +
		`<a href="http://example.com/y">http</a>` +
		`<a href="mailto:a@example.com">mail</a>` +
		`<a href="/local/path">rel</a>`
	got := sanitizePlain(t, content)

	for _, want := range []string{"https://example.com/x", "http://example.com/y", "mailto:a@example.com", "/local/path"} {
		if !strings.Contains(got, want) {
			t.Errorf("安全链接 %s 应保留: %s", want, got)
		}
	}
}

// TestSanitizeRejectsProtocolRelativeURL 确认 //host 形式被拒绝。
//
// 协议相对 URL 会继承页面协议，是绕过"只允许 http(s) 绝对地址"的常见手法。
func TestSanitizeRejectsProtocolRelativeURL(t *testing.T) {
	got := sanitizePlain(t, `<a href="//evil.test/x">x</a><img src="//evil.test/y.png">`)

	if strings.Contains(got, "evil.test") {
		t.Errorf("协议相对 URL 应被移除: %s", got)
	}
}

// TestSanitizeStripsSrcsetAndStyle 确认 srcset 与 style 被移除。
//
// srcset 能让浏览器绕过 src 直接加载外部图片，style 里的 url() 同理。
func TestSanitizeStripsSrcsetAndStyle(t *testing.T) {
	got := sanitizePlain(t, `<img src="http://example.com/a.png" srcset="http://evil.test/b.png 2x" style="background:url(http://evil.test/c.png)">`)

	if strings.Contains(got, "srcset") || strings.Contains(got, "evil.test") || strings.Contains(got, "style=") {
		t.Errorf("srcset/style 应被移除: %s", got)
	}
	if !strings.Contains(got, "http://example.com/a.png") {
		t.Errorf("合法 src 应保留: %s", got)
	}
}

// TestSanitizeRejectsDataHTMLImage 确认 data:text/html 不会被当作图片。
func TestSanitizeRejectsDataHTMLImage(t *testing.T) {
	got := sanitizePlain(t, `<img src="data:text/html;base64,PHNjcmlwdD4=">`)

	if strings.Contains(got, "data:text/html") {
		t.Errorf("非图片 data URL 应被移除: %s", got)
	}
}

// TestSanitizeKeepsDataImage 确认 data:image 保留（关闭缓存时浏览器直接渲染）。
func TestSanitizeKeepsDataImage(t *testing.T) {
	got := sanitizePlain(t, `<img src="data:image/png;base64,iVBORw0KGgo=">`)

	if !strings.Contains(got, "data:image/png;base64,iVBORw0KGgo=") {
		t.Errorf("data:image 应保留: %s", got)
	}
}

// TestSanitizeUnwrapsUnknownTags 确认白名单外但安全的标签保留文字。
//
// `<font>` 这类标签应被去掉、文字留下，而不是整段删除。
func TestSanitizeUnwrapsUnknownTags(t *testing.T) {
	got := sanitizePlain(t, `<article><font color="red">inner text</font></article>`)

	if !strings.Contains(got, "inner text") {
		t.Errorf("未知标签的文字应保留: %s", got)
	}
	if strings.Contains(got, "<font") || strings.Contains(got, "<article") {
		t.Errorf("未知标签应被去掉: %s", got)
	}
	if strings.Contains(got, "color=") {
		t.Errorf("未知标签的属性应被去掉: %s", got)
	}
}

// TestSanitizeAddsRelToBlankTarget 确认 target=_blank 自动补 rel。
func TestSanitizeAddsRelToBlankTarget(t *testing.T) {
	got := sanitizePlain(t, `<a href="https://example.com" target="_blank">x</a>`)

	if !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Errorf("target=_blank 应补 rel: %s", got)
	}
}

// TestSanitizeRejectsUnknownTarget 确认非标准 target 被移除。
func TestSanitizeRejectsUnknownTarget(t *testing.T) {
	got := sanitizePlain(t, `<a href="https://example.com" target="customName">x</a>`)

	if strings.Contains(got, "customName") {
		t.Errorf("非标准 target 应被移除: %s", got)
	}
}

// TestSanitizeKeepsTableAttributes 确认表格结构属性保留。
func TestSanitizeKeepsTableAttributes(t *testing.T) {
	got := sanitizePlain(t, `<table><tr><td colspan="2" rowspan="3" bgcolor="red">cell</td></tr></table>`)

	if !strings.Contains(got, `colspan="2"`) || !strings.Contains(got, `rowspan="3"`) {
		t.Errorf("colspan/rowspan 应保留: %s", got)
	}
	if strings.Contains(got, "bgcolor") {
		t.Errorf("未登记的属性应被移除: %s", got)
	}
}

// TestSanitizeEscapesText 确认纯文本被正确转义，不会变成标签。
func TestSanitizeEscapesText(t *testing.T) {
	got := sanitizePlain(t, `2 < 3 && "quoted"`)

	if strings.Contains(got, "<3") {
		t.Errorf("裸 < 应被转义: %s", got)
	}
	if !strings.Contains(got, "&lt;") {
		t.Errorf("应输出转义后的实体: %s", got)
	}
}

// TestSanitizeRemovesComments 确认注释被移除。
func TestSanitizeRemovesComments(t *testing.T) {
	got := sanitizePlain(t, `<p>a</p><!-- hidden comment --><p>b</p>`)

	if strings.Contains(got, "hidden comment") {
		t.Errorf("注释应被移除: %s", got)
	}
}

// TestSanitizeDropsVideoAudio 确认 video/audio/source 不会保留外链加载。
func TestSanitizeDropsVideoAudio(t *testing.T) {
	got := sanitizePlain(t, `<video src="http://evil.test/v.mp4"><source src="http://evil.test/s.mp4"></video><p>after</p>`)

	if strings.Contains(got, "evil.test") || strings.Contains(got, "<video") || strings.Contains(got, "<source") {
		t.Errorf("video/source 应被移除: %s", got)
	}
	if !strings.Contains(got, "after") {
		t.Errorf("后续正文不应受影响: %s", got)
	}
}

// TestSanitizeDropsSVG 确认内联 svg 被删除（可内嵌 script）。
func TestSanitizeDropsSVG(t *testing.T) {
	got := sanitizePlain(t, `<svg><script>alert(1)</script></svg><p>after</p>`)

	if strings.Contains(got, "svg") || strings.Contains(got, "alert") {
		t.Errorf("svg 应被删除: %s", got)
	}
	if !strings.Contains(got, "after") {
		t.Errorf("后续正文不应受影响: %s", got)
	}
}

// TestSanitizePreservesNestedStructure 确认正常排版标签保留。
func TestSanitizePreservesNestedStructure(t *testing.T) {
	content := `<h2>标题</h2><p>正文<strong>加粗</strong><em>斜体</em></p><ul><li>一</li><li>二</li></ul><pre><code>x := 1</code></pre>`
	got := sanitizePlain(t, content)

	for _, want := range []string{"<h2>", "<strong>", "<em>", "<ul>", "<li>", "<pre>", "<code>"} {
		if !strings.Contains(got, want) {
			t.Errorf("排版标签 %s 应保留: %s", want, got)
		}
	}
}

// TestExternalImageSourcesSkipsLocal 确认相对路径与 data:image 不进入缓存队列。
func TestExternalImageSourcesSkipsLocal(t *testing.T) {
	doc, ok := parseContent(`<img src="http://example.com/a.png"><img src="/local.png"><img src="data:image/png;base64,AA"><img src="http://example.com/a.png">`)
	if !ok {
		t.Fatal("解析失败")
	}

	urls := doc.externalImageSources()
	if len(urls) != 1 || urls[0] != "http://example.com/a.png" {
		t.Errorf("应只返回去重后的一个外链，实际 %v", urls)
	}
}
