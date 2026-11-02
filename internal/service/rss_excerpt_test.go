package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 摘要要同时满足三件事：不夹带标签、不把隐藏内容读进来、
// 截断不切碎多字节字符。任意一条失守，前端要么显示垃圾，
// 要么收到非法 UTF-8。

func TestExcerptReturnsReadableText(t *testing.T) {
	got := excerptFromHTML(`<p>hello</p><p>world</p>`, 300)
	if got != "hello world" {
		t.Fatalf("块级元素之间必须留出空格，got %q", got)
	}
}

func TestExcerptDropsScriptAndStyleContent(t *testing.T) {
	// script/style 的文本在浏览器里不可见；若被摘要带出来，
	// 用户会在列表里看到一整段 JS。
	got := excerptFromHTML(`<style>.a{color:red}</style><p>正文</p><script>alert(1)</script>`, 300)
	if got != "正文" {
		t.Fatalf("不可见标签的内容必须跳过，got %q", got)
	}
}

func TestExcerptNeverEmitsTags(t *testing.T) {
	got := excerptFromHTML(`<div><strong>粗体</strong>与<a href="https://example.com">链接</a></div>`, 300)
	if strings.ContainsAny(got, "<>") {
		t.Fatalf("摘要必须是纯文本，got %q", got)
	}
	if got != "粗体与链接" {
		t.Fatalf("内联元素不应引入空格，got %q", got)
	}
}

func TestExcerptTruncatesOnRuneBoundary(t *testing.T) {
	// 300 个汉字是 900 字节。按字节截断会产生非法 UTF-8，
	// 存库时整批被 PostgreSQL 拒收。
	content := "<p>" + strings.Repeat("中", entryExcerptRunes+50) + "</p>"
	got := excerptFromHTML(content, entryExcerptRunes)
	if !utf8.ValidString(got) {
		t.Fatal("摘要必须是合法 UTF-8")
	}
	if count := utf8.RuneCountInString(got); count > entryExcerptRunes {
		t.Fatalf("摘要超长：%d > %d", count, entryExcerptRunes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("被截断时要有省略号，got %q", got[len(got)-10:])
	}
}

func TestExcerptEmptyContentStaysEmpty(t *testing.T) {
	for _, content := range []string{"", "   ", "<p> </p>"} {
		if got := excerptFromHTML(content, 300); got != "" {
			t.Fatalf("空正文不该产出摘要，输入 %q got %q", content, got)
		}
	}
}

func TestStripTagsFallbackKeepsVisibleText(t *testing.T) {
	got := stripTagsFallback(`<p>第一段</p><p>第二段</p>`)
	if got != "第一段 第二段" {
		t.Fatalf("兜底路径不应丢掉可见文本，got %q", got)
	}
	// 畸形输入（未闭合的尖括号）不能把标签文本带出来。
	if got := stripTagsFallback(`a < b`); strings.ContainsAny(got, "<>") {
		t.Fatalf("兜底路径必须去掉标签，got %q", got)
	}
}
