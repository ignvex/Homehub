package service

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 列表接口不再返回整篇正文。这条断言防的是「某天有人把查询改回
// SELECT * 或 model.RSSEntry，接口悄悄又变重」——那种回退不会让
// 任何测试变红，只会让首屏响应体悄悄涨几十倍。
func TestListEntriesSelectsExcerptHeadInsteadOfFullContent(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT COUNT`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT e.id FROM`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))

	// 关键：列清单里有 LEFT(content, 4000) 与 has_content，
	// 而不是整段 content。正则不匹配时 sqlmock 会直接报「非预期查询」。
	mock.ExpectQuery(`(?s)SELECT.*LEFT\(content, 4000\).*`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "title", "link", "author", "magnet_url", "published", "fetched_at",
			"magnet_locked", "magnet_locked_at", "pikpak_status", "pikpak_task_id",
			"pikpak_error", "pikpak_saved_at", "content_head", "has_content",
		}).AddRow(
			7, "标题", "https://example.com/7", "作者", "", now, now,
			false, nil, "", "", "", nil,
			"<p>这是摘要，含 <b>标签</b>。</p>", true,
		))
	mock.ExpectQuery(`(?s)SELECT.*rss_entry_feeds`).
		WillReturnRows(sqlmock.NewRows([]string{"entry_id", "feed_id"}).AddRow(7, 3))

	svc := &RSSService{db: db}
	views, total, err := svc.ListEntries(EntryFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListEntries 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
	if total != 1 || len(views) != 1 {
		t.Fatalf("期望 1 条，got total=%d len=%d", total, len(views))
	}
	if views[0].Excerpt != "这是摘要，含 标签。" {
		t.Fatalf("摘要是纯文本，got %q", views[0].Excerpt)
	}
	if !views[0].HasContent {
		t.Fatal("has_content 应透传，前端靠它决定是否显示「展开正文」")
	}
}

// 条目可能在被点开的瞬间被 retention 清掉。若这里把 ErrRecordNotFound
// 当成内部错误，前端会看到一个 500，而不是「这条已经不在了」。
func TestEntryContentReportsMissingEntry(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`(?s)SELECT.*content.*FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows([]string{"content"}))

	svc := &RSSService{db: db}
	if _, err := svc.EntryContent(404); err != ErrRSSEntryNotFound {
		t.Fatalf("期望 ErrRSSEntryNotFound，got %v", err)
	}
}

// 磁力与 PikPak 列名是 pikpak_status 这类全小写形式，而 GORM 默认会把
// Go 字段 PikPakStatus 推断成 pik_pak_status。两者对不上时 Scan 不报错，
// 只是把值丢掉——列表里「已锁定」「已提交」会静默变成全空。
// 这条断言防的正是那种没有报错的静默丢失。
func TestListEntriesScansMagnetAndPikPakColumns(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT COUNT`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT e.id FROM`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery(`(?s)SELECT.*content_head.*`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "title", "link", "author", "magnet_url", "published", "fetched_at",
			"magnet_locked", "magnet_locked_at", "pikpak_status", "pikpak_task_id",
			"pikpak_error", "pikpak_saved_at", "content_head", "has_content",
		}).AddRow(
			7, "标题", "https://example.com/7", "", "magnet:?xt=urn:btih:abc", now, now,
			true, now, "saved", "task-1", "boom", now,
			"<p>摘要</p>", true,
		))
	mock.ExpectQuery(`(?s)SELECT.*rss_entry_feeds`).
		WillReturnRows(sqlmock.NewRows([]string{"entry_id", "feed_id"}).AddRow(7, 3))

	svc := &RSSService{db: db}
	views, _, err := svc.ListEntries(EntryFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListEntries 失败: %v", err)
	}
	got := views[0]
	if got.MagnetURL == "" {
		t.Fatal("magnet_url 丢了")
	}
	if !got.MagnetLocked {
		t.Fatal("magnet_locked 丢了：锁定状态会显示成未锁")
	}
	if got.MagnetLockedAt == nil {
		t.Fatal("magnet_locked_at 丢了")
	}
	if got.PikPakStatus != "saved" || got.PikPakTaskID != "task-1" || got.PikPakError != "boom" || got.PikPakSavedAt == nil {
		t.Fatalf("PikPak 状态被静默丢弃：%+v", got)
	}
}
