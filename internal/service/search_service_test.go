package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLikePatternEscapesWildcards(t *testing.T) {
	// 用户输入的是字面量。不转义的话，搜 "100%" 会变成
	// 「以 100 开头的任意内容」，搜 "_" 会匹配任意单字符。
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"百分号被转义", "100%", `%100\%%`},
		{"下划线被转义", "a_b", `%a\_b%`},
		{"反斜杠被转义", `a\b`, `%a\\b%`},
		{"普通词只包通配符", "deepseek", "%deepseek%"},
		{"中文原样", "余额", "%余额%"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := likePattern(tc.input); got != tc.want {
				t.Fatalf("likePattern(%q) = %q，期望 %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeSearchOptionsClampsPaging(t *testing.T) {
	// per_page 上限防止一次请求把整库拉进内存导致 SQL 超时——
	// 那种失败在前端看起来只是「搜索很慢」，很难归因。
	cases := []struct {
		name        string
		in          SearchOptions
		wantPage    int
		wantPerPage int
	}{
		{"缺省值", SearchOptions{}, 1, DefaultSearchPerPage},
		{"零页归一", SearchOptions{Page: 0}, 1, DefaultSearchPerPage},
		{"负页归一", SearchOptions{Page: -5}, 1, DefaultSearchPerPage},
		{"超限被夹", SearchOptions{Page: 1, PerPage: 5000}, 1, MaxSearchPerPage},
		{"合法值保留", SearchOptions{Page: 3, PerPage: 50}, 3, 50},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSearchOptions(tc.in)
			if got.Page != tc.wantPage || got.PerPage != tc.wantPerPage {
				t.Fatalf("page/per_page = %d/%d，期望 %d/%d",
					got.Page, got.PerPage, tc.wantPage, tc.wantPerPage)
			}
		})
	}
}

func TestBuildSnippetKeepsUTF8Intact(t *testing.T) {
	// 摘要按字节切开多字节字符会产出替换字符（U+FFFD）。
	// 这是 PostgreSQL 拒收整批写入的同一类问题在展示层的表现。
	content := "<p>" + strings.Repeat("前文内容。", 40) + "关键词在这里" + strings.Repeat("后文内容。", 40) + "</p>"

	got := buildSnippet(content, "关键词")
	if got == "" {
		t.Fatal("命中关键词时应给出摘要")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("摘要不是合法 UTF-8：%q", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("摘要含有替换字符，说明切碎了多字节字符：%q", got)
	}
	if !strings.Contains(got, "关键词") {
		t.Fatalf("摘要应包含关键词，实际 %q", got)
	}
	// 省略号表明被截断过，让人知道这不是全文开头。
	if !strings.HasPrefix(got, "…") {
		t.Fatalf("从中间截取时应加前导省略号，实际 %q", got)
	}
}

func TestBuildSnippetStripsHTML(t *testing.T) {
	// 正文是清洗后的 HTML，摘要里不该出现标签——前端会把它当纯文本渲染。
	got := buildSnippet("<p><strong>关键词</strong> 后面还有内容</p>", "关键词")
	if strings.ContainsRune(got, '<') || strings.ContainsRune(got, '>') {
		t.Fatalf("摘要不应含 HTML 标签，实际 %q", got)
	}
	if !strings.Contains(got, "关键词") {
		t.Fatalf("摘要应包含关键词，实际 %q", got)
	}
}

func TestBuildSnippetReturnsEmptyWhenNoMatch(t *testing.T) {
	// 找不到关键词时返回空串，而不是正文开头。
	// 给一段无关的开头会让人以为搜错了——前端据此区分
	// 「命中在正文」与「命中在标题」。
	if got := buildSnippet("<p>完全无关的内容</p>", "关键词"); got != "" {
		t.Fatalf("未命中正文时应返回空串，实际 %q", got)
	}
}

func TestBuildSnippetIsCaseInsensitive(t *testing.T) {
	got := buildSnippet("<p>DeepSeek is great</p>", "deepseek")
	if got == "" {
		t.Fatal("大小写不同也应命中")
	}
}
