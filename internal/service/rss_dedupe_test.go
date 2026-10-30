package service

import (
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

// TestEntriesFromItemsDeduplicatesByLink 锁住一条高危回归。
//
// 返回值会作为一条多行 INSERT ... ON CONFLICT DO UPDATE 的输入。
// PostgreSQL 不允许同一语句内两行命中同一个冲突键，会报
//
//	21000: ON CONFLICT DO UPDATE command cannot affect row a second time
//
// 整个批次回滚 → 该源每一轮抓取都失败，一条新条目都进不来，且不自愈。
// 而源站重复发布同一条链接是很常见的现象。
func TestEntriesFromItemsDeduplicatesByLink(t *testing.T) {
	s := &RSSService{}
	now := time.Now()

	items := []*gofeed.Item{
		{Title: "第一篇", Link: "https://example.com/a"},
		{Title: "重复的 A", Link: "https://example.com/a"}, // 同一 link
		{Title: "第二篇", Link: "https://example.com/b"},
		{Title: "又一个 A", Link: "https://example.com/a"},
	}

	entries, publishedByLink := s.entriesFromItems(items, now)

	if len(entries) != 2 {
		t.Fatalf("去重后条目数 = %d，期望 2（实际 %d 条会让 upsert 报 21000）",
			len(entries), len(entries))
	}
	if entries[0].Link != "https://example.com/a" || entries[1].Link != "https://example.com/b" {
		t.Errorf("去重后顺序/内容不符: %v", []string{entries[0].Link, entries[1].Link})
	}
	// 保留首次出现：标题不应是后来那条重复的。
	if entries[0].Title != "第一篇" {
		t.Errorf("应保留首次出现的条目，标题 = %q", entries[0].Title)
	}
	if len(publishedByLink) != 2 {
		t.Errorf("publishedByLink 条目数 = %d，期望 2", len(publishedByLink))
	}
}

// TestEntriesFromItemsSkipsEmptyLink 确认无链接条目被跳过，
// 否则会以空串进入 link 唯一索引，把整批插入拖垮。
func TestEntriesFromItemsSkipsEmptyLink(t *testing.T) {
	s := &RSSService{}

	items := []*gofeed.Item{
		{Title: "没有链接", Link: ""},
		{Title: "正常", Link: "https://example.com/x"},
	}

	entries, _ := s.entriesFromItems(items, time.Now())

	if len(entries) != 1 {
		t.Fatalf("条目数 = %d，期望 1", len(entries))
	}
	if entries[0].Link != "https://example.com/x" {
		t.Errorf("保留了错误的条目: %q", entries[0].Link)
	}
}

// TestEntriesFromItemsTruncationCollapse 确认截断后撞成同一 link 的条目
// 也会被去重，而不是留给数据库报 21000。
//
// 两个不同的超长 URL 在截断到列上限后可能得到同一个值。
func TestEntriesFromItemsTruncationCollapse(t *testing.T) {
	s := &RSSService{}
	const base = "https://example.com/"

	// 构造两个前缀相同、但尾部不同的长链接，使其截断后相同。
	long1 := base + repeatStr("x", 2100) + "FIRST"
	long2 := base + repeatStr("x", 2100) + "SECOND"

	items := []*gofeed.Item{
		{Title: "1", Link: long1},
		{Title: "2", Link: long2},
	}

	entries, _ := s.entriesFromItems(items, time.Now())

	// 若两者截断后不同则保留两条；若相同则必须只剩一条。
	// 断言的是"不会出现两条相同 link"，而不是具体的条数。
	if len(entries) == 2 && entries[0].Link == entries[1].Link {
		t.Fatalf("两条条目截断后 link 相同，会触发 upsert 21000 错误")
	}
}

func repeatStr(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
