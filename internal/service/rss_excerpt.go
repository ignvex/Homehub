package service

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/ignvex/HomeHub/internal/model"
)

// 本文件生成条目列表用的纯文本摘要。
//
// 列表接口返回整篇正文的代价随条目数线性增长：一页 20 条、每条正文
// 可能上百 KB，而这些内容绝大多数时候折叠着没人看。改成「列表只带
// 摘要，展开正文时再单独取」之后，首屏传输只与标题和摘要的长度有关。
//
// 摘要必须是**纯文本**：它由前端当文本节点渲染，若把 HTML 原样塞进
// 来，要么显示成一堆标签，要么诱导调用方 dangerouslySetInnerHTML。

// entryExcerptRunes 是列表摘要的字符上限（rune，不是字节）。
//
// 300 与首页卡片原有的摘要长度一致：摘要只回答「这条要不要展开」，
// 超过三行就不再帮助判断，只是把列表撑高。
const entryExcerptRunes = 300

// excerptHiddenTags 是在提取摘要时连内容一起跳过的标签。
//
// 与正文清洗的 dropTags 不是同一套：这里只关心「读起来有没有意义」，
// 不承担安全职责——正文在写库前已经过 sanitizeContent。
var excerptHiddenTags = map[string]struct{}{
	"script": {}, "style": {}, "head": {}, "title": {}, "noscript": {},
	"template": {}, "svg": {}, "math": {}, "iframe": {}, "object": {},
	"embed": {}, "canvas": {}, "audio": {}, "video": {},
}

// excerptBlockTags 是需要在文本流里断开的标签。
//
// 不加这层的话 `<p>第一段</p><p>第二段</p>` 会粘成「第一段第二段」，
// 中文没有空格也还能读，但中英混排的站点会出现「foo.Bar」这种粘连。
var excerptBlockTags = map[string]struct{}{
	"br": {}, "p": {}, "div": {}, "li": {}, "ul": {}, "ol": {}, "dl": {},
	"dt": {}, "dd": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {},
	"h6": {}, "blockquote": {}, "pre": {}, "table": {}, "thead": {},
	"tbody": {}, "tr": {}, "td": {}, "th": {}, "figcaption": {},
	"section": {}, "article": {}, "header": {}, "footer": {}, "hr": {},
}

// excerptFromHTML 从已入库的正文里提取纯文本摘要。
//
// 解析失败（二进制垃圾、被硬切的 UTF-8）时退化为「去掉尖括号里的内容」
// 的兜底路径，而不是原样返回：调用方拿到的字段承诺是可读文本。
func excerptFromHTML(content string, maxRunes int) string {
	if maxRunes <= 0 || strings.TrimSpace(content) == "" {
		return ""
	}

	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(content), root)
	if err != nil {
		return model.TruncateEllipsis(stripTagsFallback(content), maxRunes)
	}

	var b strings.Builder
	for _, node := range nodes {
		appendExcerptText(&b, node)
	}
	// 折叠空白：换行、缩进、连续空格在摘要里没有意义，只会占掉字符配额。
	text := strings.Join(strings.Fields(b.String()), " ")
	return model.TruncateEllipsis(text, maxRunes)
}

// appendExcerptText 递归收集节点的可见文本。
func appendExcerptText(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		if _, hidden := excerptHiddenTags[n.Data]; hidden {
			return
		}
		if _, block := excerptBlockTags[n.Data]; block {
			b.WriteByte(' ')
		}
	case html.DocumentNode:
		// ParseFragment 的顶层容器，直接下钻。
	default:
		return
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		appendExcerptText(b, child)
	}
	if _, block := excerptBlockTags[n.Data]; block {
		b.WriteByte(' ')
	}
}

// stripTagsFallback 是解析失败时的最后一道兜底：粗粒度地去掉标签。
//
// 这里不追求正确性——真正的解析已经失败，能做的只是别把整段 HTML
// 当摘要返回。尖括号内容被丢弃，文本节点原样保留，交给 TruncateEllipsis
// 处理长度与非法 UTF-8。
func stripTagsFallback(content string) string {
	var b strings.Builder
	depth := 0
	for _, r := range content {
		switch {
		case r == '<':
			depth++
			b.WriteByte(' ')
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
