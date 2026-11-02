package service

import (
	"strings"
	"testing"
)

// TestExtractMagnetDecodesHTMLEntities 锁住实体解码。
//
// 源站把 href 写成 `magnet:?xt=...&amp;dn=...&amp;tr=...` 是常态。
// 不解码直接提取，结果里会带 `&amp;` 字面量：粘到 PikPak 或 BT 客户端
// 时参数名变成 `amp;tr`，所有 tracker 失效。
//
// 为什么这个 bug 容易漏：`xt` 在第一个参数位、没有被破坏，客户端仍能
// 从种子哈希识别出资源，看上去「能用」，只是永远找不到 peers。
func TestExtractMagnetDecodesHTMLEntities(t *testing.T) {
	const raw = `<a class="download-arrow arrow-magnet" title="磁力下載" ` +
		`href="magnet:?xt=urn:btih:ZA6RKOQA3623WR3MJZ2PVAQCLK7CGCVS&amp;dn=&amp;tr=http%3A%2F%2F104.143.10.61%3A8000%2Fannounce">` +
		`&nbsp;</a>`

	got := extractMagnet(raw)

	if got == "" {
		t.Fatal("应能从这段 HTML 里提取出磁力链接")
	}
	if strings.Contains(got, "&amp;") {
		t.Errorf("实体未被解码，tracker 参数名会变成 amp;tr: %s", got)
	}
	if !strings.Contains(got, "&tr=http%3A%2F%2F104.143.10.61%3A8000%2Fannounce") {
		t.Errorf("tracker 参数应完整保留: %s", got)
	}
	if !strings.HasPrefix(got, "magnet:?xt=urn:btih:ZA6RKOQA3623WR3MJZ2PVAQCLK7CGCVS") {
		t.Errorf("种子哈希应位于链接开头: %s", got)
	}
}

// TestExtractMagnetTakesFirstAndStopsAtQuotes 确认边界。
//
// 正文里常有多条磁力链接，抓取时应取第一条；同时不能越过引号
// 把后面的标签文本一起吃进来。
func TestExtractMagnetTakesFirstAndStopsAtQuotes(t *testing.T) {
	in := `first <a href="magnet:?xt=urn:btih:AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH&dn=one">x</a>` +
		` second <a href="magnet:?xt=urn:btih:11112222333344445555666677778888&dn=two">y</a>`

	got := extractMagnet(in)

	if !strings.Contains(got, "AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH") {
		t.Errorf("应取第一条: %s", got)
	}
	if strings.Contains(got, "two") || strings.Contains(got, "second") {
		t.Errorf("不应跨界取到后面的内容: %s", got)
	}
}

// TestExtractMagnetReturnsEmptyWhenAbsent 确认没有磁力时返回空串。
//
// 调用方靠空串判断「这条没有被提取到」，返回垃圾值会让 UI 出现
// 点了没反应的按钮。
func TestExtractMagnetReturnsEmptyWhenAbsent(t *testing.T) {
	for _, in := range []string{
		"",
		"<p>纯文本，没有磁力</p>",
		`<a href="magnet:?dn=missing-xt">缺 xt 参数</a>`,
		`<a href="ed2k://|file|x|y|z|/">别的协议</a>`,
	} {
		if got := extractMagnet(in); got != "" {
			t.Errorf("输入 %q 不应提取到磁力，实际 %q", in, got)
		}
	}
}
