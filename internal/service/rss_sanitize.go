package service

import (
	stdhtml "html"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// 本文件负责条目正文的 HTML 清洗。
//
// 旧实现用一串正则删 script/iframe、剥 src、替换 style url()。正则解析
// HTML 天然不可靠：`<img src=x onerror=alert(1)>` 里的 on* 属性、
// `srcset`、`<a href="javascript:...">`、大小写变体、畸形嵌套都能绕过。
// RSS 正文完全由第三方控制，因此改为解析成 DOM 树后按白名单重建：
//
//   - 不在白名单的标签：安全标签保留子节点（unwrap），危险标签整棵删除
//   - 属性按 标签+全局 两级白名单过滤，其余一律丢弃（含 on*、style、srcset）
//   - 链接与图片地址校验协议，禁止 javascript:/vbscript:/协议相对 URL
//   - 输出由 html.Render 统一转义，不手工拼字符串

// allowedTags 是允许保留的标签白名单。
//
// 只收排版、表格、代码这类静态内容。刻意不收 video/audio/source/picture：
// 它们会带 src 直接加载外部资源，绕过图片缓存与大小限制。
var allowedTags = map[string]struct{}{
	"a": {}, "abbr": {}, "b": {}, "blockquote": {}, "br": {}, "caption": {},
	"cite": {}, "code": {}, "dd": {}, "del": {}, "div": {}, "dl": {}, "dt": {},
	"em": {}, "figcaption": {}, "figure": {}, "h1": {}, "h2": {}, "h3": {},
	"h4": {}, "h5": {}, "h6": {}, "hr": {}, "i": {}, "img": {}, "ins": {},
	"kbd": {}, "li": {}, "mark": {}, "ol": {}, "p": {}, "pre": {}, "q": {},
	"s": {}, "samp": {}, "small": {}, "span": {}, "strong": {}, "sub": {},
	"sup": {}, "table": {}, "tbody": {}, "td": {}, "tfoot": {}, "th": {},
	"thead": {}, "tr": {}, "u": {}, "ul": {}, "var": {},
}

// dropTags 是必须连同内容一起删除的标签：删标签留内容会留下脚本文本，
// 或者让 <style>/<svg> 的内容变成可见垃圾。
var dropTags = map[string]struct{}{
	"script": {}, "style": {}, "iframe": {}, "object": {}, "embed": {},
	"frame": {}, "frameset": {}, "applet": {}, "form": {}, "input": {},
	"button": {}, "textarea": {}, "select": {}, "option": {}, "optgroup": {},
	"meta": {}, "link": {}, "base": {}, "title": {}, "head": {}, "svg": {},
	"math": {}, "template": {}, "noscript": {}, "canvas": {}, "map": {},
	"area": {}, "param": {}, "source": {}, "track": {}, "audio": {}, "video": {},
}

// allowedAttrs 声明各标签允许保留的属性。
//
// "*" 是全局属性。不包含 style/on*/srcset/id：style 可带 url() 与
// expression，srcset 能绕过图片缓存，id 会与页面自身元素冲突。
var allowedAttrs = map[string]map[string]struct{}{
	"*": {
		"title": {}, "lang": {}, "dir": {}, "class": {},
	},
	"a": {
		"href": {}, "target": {},
	},
	"img": {
		"src": {}, "alt": {}, "width": {}, "height": {},
	},
	"td": {"colspan": {}, "rowspan": {}},
	"th": {"colspan": {}, "rowspan": {}},
	"ol": {"start": {}, "reversed": {}},
}

// contentDoc 是一棵清洗过的正文 DOM 树。
type contentDoc struct {
	// root 是 ParseFragment 使用的上下文节点，渲染时遍历它的子节点，
	// 这样被 unwrap 的顶层节点不会重复输出。
	root   *html.Node
	images []*html.Node
}

// parseContent 解析并清洗 HTML。
//
// 返回 ok=false 表示内容无法解析成 HTML（例如二进制垃圾）；调用方应
// 退化为纯文本转义输出，而不是原样保留。
func parseContent(content string) (*contentDoc, bool) {
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(content), root)
	if err != nil {
		return nil, false
	}
	for _, n := range nodes {
		root.AppendChild(n)
	}

	doc := &contentDoc{root: root}
	for child := root.FirstChild; child != nil; {
		next := child.NextSibling
		doc.clean(child)
		child = next
	}
	return doc, true
}

// clean 递归清洗一个节点。
//
// 先处理子节点再处理自身：删除节点时子树已经清理完毕，不会留下悬挂引用。
func (d *contentDoc) clean(n *html.Node) {
	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		d.clean(child)
		child = next
	}

	switch n.Type {
	case html.TextNode:
		return
	case html.ElementNode:
		// 继续往下走
	default:
		// 注释、doctype、错误节点
		removeNode(n)
		return
	}

	tag := strings.ToLower(n.Data)
	if _, bad := dropTags[tag]; bad {
		removeNode(n)
		return
	}

	if _, ok := allowedTags[tag]; !ok {
		// 安全但不在白名单：保留文字内容，去掉标签本身。
		unwrapNode(n)
		return
	}

	d.filterAttrs(n, tag)
	if tag == "img" {
		d.images = append(d.images, n)
	}
}

// filterAttrs 按白名单过滤属性。
func (d *contentDoc) filterAttrs(n *html.Node, tag string) {
	global := allowedAttrs["*"]
	specific := allowedAttrs[tag]

	kept := n.Attr[:0]
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		_, okGlobal := global[key]
		_, okTag := specific[key]
		if !okGlobal && !okTag {
			continue
		}

		switch key {
		case "href":
			value, ok := safeLinkURL(attr.Val)
			if !ok {
				continue
			}
			attr.Val = value
		case "src":
			value, ok := safeImageSource(attr.Val)
			if !ok {
				continue
			}
			attr.Val = value
		case "target":
			// 只允许新窗口/当前窗口，避免奇怪的命名上下文。
			if attr.Val != "_blank" && attr.Val != "_self" {
				continue
			}
		}

		kept = append(kept, attr)
	}
	n.Attr = kept

	// 新窗口链接补 rel，防止被打开的页面通过 window.opener 改回跳转。
	if tag == "a" && hasAttr(n, "target", "_blank") && !hasAttrKey(n, "rel") {
		n.Attr = append(n.Attr, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
	}
}

// safeLinkURL 校验 <a href>：只放行 http/https/mailto 与站内相对路径。
func safeLinkURL(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}

	u, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	if u.Scheme == "" {
		// 禁止 //evil.com 这类协议相对 URL：它会继承当前页面的协议。
		if strings.HasPrefix(value, "//") {
			return "", false
		}
		return value, true
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return value, true
	}
	return "", false
}

// safeImageSource 校验 <img src>：放行 http/https、站内相对路径与 data:image。
//
// data: 只允许 image/*，避免 data:text/html 被当作图片渲染。
func safeImageSource(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}

	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "data:") {
		if strings.HasPrefix(lower, "data:image/") {
			return value, true
		}
		return "", false
	}
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "vbscript:") {
		return "", false
	}
	if strings.HasPrefix(value, "//") {
		return "", false
	}
	if strings.HasPrefix(value, "/") {
		return value, true
	}

	u, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	if u.Scheme == "" {
		return value, true
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return value, true
	}
	return "", false
}

// externalImageSources 返回需要本地缓存的外链图片地址（去重、保持顺序）。
func (d *contentDoc) externalImageSources() []string {
	seen := make(map[string]struct{}, len(d.images))
	urls := make([]string, 0, len(d.images))

	for _, img := range d.images {
		src := attrValue(img, "src")
		if !isExternalHTTPURL(src) {
			continue
		}
		if _, ok := seen[src]; ok {
			continue
		}
		seen[src] = struct{}{}
		urls = append(urls, src)
	}
	return urls
}

// applyImageMapping 用缓存结果改写图片地址；没有映射的外链图片直接移除。
//
// mapping 为空表示缓存被关闭或全部失败 —— 关闭缓存时调用方不会调用本
// 方法，所以这里的"没有映射"一律按失败处理。
func (d *contentDoc) applyImageMapping(mapping map[string]string) {
	for _, img := range d.images {
		src := attrValue(img, "src")

		// 站内相对路径与 data:image 不参与缓存，保持原样。
		if src == "" || strings.HasPrefix(src, "/") ||
			strings.HasPrefix(strings.ToLower(src), "data:") {
			if src == "" {
				removeNode(img)
			}
			continue
		}

		if !isExternalHTTPURL(src) {
			removeNode(img)
			continue
		}

		if local, ok := mapping[src]; ok {
			setAttr(img, "src", local)
			continue
		}
		removeNode(img)
	}
}

// render 输出清洗后的 HTML。
func (d *contentDoc) render() string {
	var b strings.Builder
	for child := d.root.FirstChild; child != nil; child = child.NextSibling {
		_ = html.Render(&b, child)
	}
	return b.String()
}

// ============================================================
// 树操作辅助
// ============================================================

// removeNode 从父节点摘除一个节点。
func removeNode(n *html.Node) {
	if n == nil || n.Parent == nil {
		return
	}
	n.Parent.RemoveChild(n)
}

// unwrapNode 用子节点替换节点本身，保留文字内容。
func unwrapNode(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		// InsertBefore 要求待插入节点未附加，必须先摘除。
		n.RemoveChild(child)
		parent.InsertBefore(child, n)
		child = next
	}
	parent.RemoveChild(n)
}

// attrValue 读取属性值（未命中返回空串）。
func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

// hasAttr 判断属性是否等于指定值。
func hasAttr(n *html.Node, key, value string) bool {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) && attr.Val == value {
			return true
		}
	}
	return false
}

// hasAttrKey 判断属性是否存在。
func hasAttrKey(n *html.Node, key string) bool {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

// setAttr 覆盖已有属性，不存在则追加。
func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

// isExternalHTTPURL 判断是否为需要走网络抓取的绝对 http(s) 地址。
func isExternalHTTPURL(raw string) bool {
	if raw == "" || strings.HasPrefix(raw, "/") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}

// escapeFallback 把无法解析的内容当纯文本输出。
func escapeFallback(content string) string {
	return stdhtml.EscapeString(content)
}
