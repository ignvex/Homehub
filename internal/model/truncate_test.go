package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateRunesKeepsValidUTF8 是这组测试的核心断言：
// 无论从哪里截断，结果都必须是合法 UTF-8 且字符数不超上限。
func TestTruncateRunesKeepsValidUTF8(t *testing.T) {
	// 中文 3 字节/字符，最容易暴露"按字节切"的 bug。
	s := strings.Repeat("中文标题", 100) // 400 字符，1200 字节

	for max := 1; max <= 420; max++ {
		got := TruncateRunes(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("TruncateRunes(max=%d) 产生了非法 UTF-8: %q", max, got)
		}
		if n := utf8.RuneCountInString(got); n > max {
			t.Fatalf("TruncateRunes(max=%d) 返回 %d 个字符，超过上限", max, n)
		}
		if !strings.HasPrefix(s, got) {
			t.Fatalf("TruncateRunes(max=%d) 结果不是原串前缀", max)
		}
	}
}

func TestTruncateRunesBoundaries(t *testing.T) {
	s := "abcdef"
	cases := []struct {
		max  int
		want string
	}{
		{0, ""},
		{-1, ""},
		{1, "a"},
		{6, "abcdef"},
		{100, "abcdef"}, // 短于上限时原样返回
	}
	for _, c := range cases {
		if got := TruncateRunes(s, c.max); got != c.want {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", s, c.max, got, c.want)
		}
	}
}

// TestTruncateRunesMixedWidth 覆盖"一个字符 1~4 字节"混合的情况。
func TestTruncateRunesMixedWidth(t *testing.T) {
	s := "a中🙂b" // 1 + 3 + 4 + 1 = 9 字节，4 个字符
	for max := 0; max <= 6; max++ {
		got := TruncateRunes(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d 产生非法 UTF-8: %q", max, got)
		}
		if n := utf8.RuneCountInString(got); n > max {
			t.Fatalf("max=%d 返回 %d 字符", max, n)
		}
	}
	if got := TruncateRunes(s, 4); got != s {
		t.Errorf("完整保留时 = %q, want %q", got, s)
	}
}

func TestTruncateEllipsis(t *testing.T) {
	s := "abcdefghij"
	if got := TruncateEllipsis(s, 5); got != "ab..." {
		t.Errorf("TruncateEllipsis(10 字符, 5) = %q, want %q", got, "ab...")
	}
	if got := TruncateEllipsis(s, 10); got != s {
		t.Errorf("未超限时应原样返回，got %q", got)
	}

	// 中文：总字符数仍不能超过上限，且保持合法 UTF-8。
	cn := strings.Repeat("中", 20)
	got := TruncateEllipsis(cn, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("产生非法 UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 10 {
		t.Errorf("字符数 = %d, want 10", n)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("应带省略号, got %q", got)
	}
}

// TestTruncateEllipsisTinyLimit 确认极小的上限不会 panic 或越界。
func TestTruncateEllipsisTinyLimit(t *testing.T) {
	for _, max := range []int{-1, 0, 1, 2, 3} {
		got := TruncateEllipsis("abcdef", max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d 产生非法 UTF-8", max)
		}
		if max >= 0 && utf8.RuneCountInString(got) > max {
			t.Fatalf("max=%d 返回 %q 超长", max, got)
		}
	}
}

// TestTruncateIndexedRespectsByteCap 针对 btree 索引字节上限的回归测试。
//
// 这是真实故障场景：2048 个中文字符 = 6144 字节，远超 btree 单键上限，
// 直接入库会让 PostgreSQL 拒绝整条语句。
func TestTruncateIndexedRespectsByteCap(t *testing.T) {
	s := strings.Repeat("中", 2048) // 6144 字节

	got := TruncateIndexed(s, 2048)
	if !utf8.ValidString(got) {
		t.Fatalf("产生非法 UTF-8")
	}
	if len(got) > pgBTreeMaxKeyBytes {
		t.Fatalf("字节数 %d 超过 btree 上限 %d", len(got), pgBTreeMaxKeyBytes)
	}
	if n := utf8.RuneCountInString(got); n > 2048 {
		t.Fatalf("字符数 %d 超过列上限 2048", n)
	}
	if !strings.HasPrefix(s, got) {
		t.Fatal("结果不是原串前缀")
	}
}

// TestTruncateIndexedPrefersCharLimitForASCII 确认纯 ASCII 时字符上限先生效。
func TestTruncateIndexedPrefersCharLimitForASCII(t *testing.T) {
	s := strings.Repeat("a", 5000)
	got := TruncateIndexed(s, 2048)
	if len(got) != 2048 {
		t.Fatalf("ASCII 应截断到 2048 字节, got %d", len(got))
	}
}

func TestTruncateIndexedShortStringUnchanged(t *testing.T) {
	s := "https://example.com/article"
	if got := TruncateIndexed(s, 2048); got != s {
		t.Errorf("短串应原样返回, got %q", got)
	}
}

// TestTruncateRepairsInvalidUTF8 锁住一条真实现故障：
//
// 入参本身可能就是非法 UTF-8。仓库里确实有这种生产者——
// rss_remote_fetcher 用 io.LimitReader(body, 512) 读错误页，512 字节
// 很容易把一个中文字符切成两半。
//
// 之前的实现只保证“不自己切坏字符”，对这种输入会走快速路径原样返回，
// 于是调用方以为截断成功，实际拿到一串不能入库的字节，导致
// UPDATE ... SET error_msg 被 PostgreSQL 拒收。
func TestTruncateRepairsInvalidUTF8(t *testing.T) {
	valid := "extension 返回 500: 服务异常"
	// 硬切到合法串中间，制造非法 UTF-8。
	broken := valid[:len(valid)-4]
	if utf8.ValidString(broken) {
		t.Fatalf("测试前提不成立：截断后仍为合法 UTF-8: %q", broken)
	}

	cases := []struct {
		name string
		got  string
	}{
		{"TruncateRunes 短串不走截断分支", TruncateRunes(broken, 1024)},
		{"TruncateRunes 走截断分支", TruncateRunes(broken, 5)},
		{"TruncateEllipsis 短串", TruncateEllipsis(broken, 1024)},
		{"TruncateEllipsis 长串", TruncateEllipsis(broken, 5)},
		{"TruncateIndexed", TruncateIndexed(broken, 2048)},
	}
	for _, c := range cases {
		if !utf8.ValidString(c.got) {
			t.Errorf("%s 仍返回非法 UTF-8: %q", c.name, c.got)
		}
	}
}

// TestTruncateRepairsInvalidUTF8KeepsContent 确认修复非法字节时
// 合法部分不被丢掉。
func TestTruncateRepairsInvalidUTF8KeepsContent(t *testing.T) {
	broken := "前缀中文" + "\xe4\xb8" // 半个“中”字
	got := TruncateRunes(broken, 100)

	if !utf8.ValidString(got) {
		t.Fatalf("结果仍非法: %q", got)
	}
	if !strings.HasPrefix(got, "前缀中文") {
		t.Errorf("合法前缀被丢弃: %q", got)
	}
	if !strings.Contains(got, "\uFFFD") {
		t.Errorf("坏字节应被替换为 U+FFFD: %q", got)
	}
}
