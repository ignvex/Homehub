package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
	"gorm.io/gorm"
)

// fakeImageCache 是 RSSImageCache 的测试替身，只记录 Sweep 调用。
type fakeImageCache struct {
	sweeps []time.Duration
}

func (f *fakeImageCache) Cache(context.Context, uint64, []string) map[string]string {
	return map[string]string{}
}

func (f *fakeImageCache) Sweep(_ context.Context, grace time.Duration) error {
	f.sweeps = append(f.sweeps, grace)
	return nil
}

// TestDeleteFeedKeepsSharedEntries 确认删除订阅源只影响自己的关联。
//
// 这是本设计的核心不变量：同一条内容被 A、B 两个源收录时，删 A 不能
// 带走内容本身，也不能带走 B 的关联和图片引用。
func TestDeleteFeedKeepsSharedEntries(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()

	// 1. 查订阅源
	mock.ExpectQuery(`SELECT \* FROM "homehub_rss_feeds" WHERE "homehub_rss_feeds"."id" = \$1`).
		WithArgs(uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "title", "url",
			"site_url", "description", "icon_url", "fetch_interval", "last_fetch",
			"error_msg", "group_id",
		}).AddRow(7, time.Now(), time.Now(), nil, "A", "http://a/feed",
			"", "", "", 3600, nil, "", nil))

	// 2. 记下本源收录的条目（含被 B 也收录的那条）
	mock.ExpectQuery(`SELECT "entry_id" FROM "homehub_rss_entry_feeds" WHERE feed_id = \$1`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"entry_id"}).AddRow(11).AddRow(12))

	// 3. 只删本源的关联
	mock.ExpectExec(`DELETE FROM "homehub_rss_entry_feeds" WHERE feed_id = \$1`).
		WithArgs(uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	// 4. 删订阅源
	mock.ExpectExec(`DELETE FROM "homehub_rss_feeds" WHERE "homehub_rss_feeds"."id" = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// 5. 判定孤儿：只有 12 号没有任何关联（11 号仍被 B 收录）
	mock.ExpectQuery(`SELECT "id" FROM "homehub_rss_entries" WHERE id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(12))

	// 6. 只删孤儿条目与它的图片引用
	mock.ExpectExec(`DELETE FROM "homehub_rss_entries" WHERE id IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM "homehub_rss_image_refs" WHERE entry_id IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	images := &fakeImageCache{}
	s := &RSSService{db: db, images: images}

	if err := s.DeleteFeed(7); err != nil {
		t.Fatalf("DeleteFeed 失败: %v", err)
	}
	if len(images.sweeps) != 1 || images.sweeps[0] != 0 {
		t.Errorf("Sweep 调用 = %v，期望一次 grace=0", images.sweeps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestEnforceRetentionCountsPerFeed 确认保留策略按"本源收录数"计数，
// 删除关联时不碰其他源仍在用的条目。
func TestEnforceRetentionCountsPerFeed(t *testing.T) {
	loadConfigWith(t, `,"rss": {"retention_count": 2}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "homehub_rss_entry_feeds" WHERE feed_id = \$1`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	mock.ExpectQuery(`SELECT "id","entry_id" FROM "homehub_rss_entry_feeds" WHERE feed_id = \$1`).
		WithArgs(uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "entry_id"}).AddRow(21, 11))

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "homehub_rss_entry_feeds" WHERE id IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 11 号仍被别的源收录 → 不是孤儿
	mock.ExpectQuery(`SELECT "id" FROM "homehub_rss_entries" WHERE id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	images := &fakeImageCache{}
	s := &RSSService{db: db, images: images}

	if err := s.enforceRetention(context.Background(), 7); err != nil {
		t.Fatalf("enforceRetention 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestEnforceRetentionNoopWithinLimit 确认未超限时不做任何删除。
func TestEnforceRetentionNoopWithinLimit(t *testing.T) {
	loadConfigWith(t, `,"rss": {"retention_count": 10}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "homehub_rss_entry_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	images := &fakeImageCache{}
	s := &RSSService{db: db, images: images}

	if err := s.enforceRetention(context.Background(), 7); err != nil {
		t.Fatalf("enforceRetention 失败: %v", err)
	}
	if len(images.sweeps) != 0 {
		t.Errorf("未超限不应触发 Sweep，实际 %v", images.sweeps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestSweepRemovesOrphanFileAndRecord 确认定期清理会删掉
// “无引用 + 超过保留期”的图片文件和数据库记录。
func TestSweepRemovesOrphanFileAndRecord(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cleanup_enabled": true, "image_retention_days": 30}`)

	dir := t.TempDir()
	filename := "rss_orphan.png"
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// 立即清理（grace=0）不跑引用对账，避免误删正在入库的条目引用。
	rows := sqlmock.NewRows([]string{
		"id", "created_at", "updated_at", "deleted_at",
		"cache_key", "source_url", "filename", "content_type",
		"size_bytes", "sha256", "last_seen_at",
	}).AddRow(
		5, old, old, nil,
		"key", "http://example.com/a.png", filename, "image/png",
		3, "", old,
	)
	mock.ExpectQuery(`SELECT \* FROM "homehub_rss_images" WHERE last_seen_at <`).
		WillReturnRows(rows)

	mock.ExpectExec(`DELETE FROM "homehub_rss_images" WHERE "homehub_rss_images"."id" =`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	if err := c.Sweep(context.Background(), 0); err != nil {
		t.Fatalf("Sweep 失败: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("孤儿文件应被删除，实际 err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestAdoptRejectsUnsafeOrMissingFiles 确认 Adopt 只登记缓存目录内
// 真实存在的文件，拒绝路径穿越。
func TestAdoptRejectsUnsafeOrMissingFiles(t *testing.T) {
	dir := t.TempDir()
	filename := "rss_ok.png"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}
	got := c.Adopt(1, []AdoptedImage{
		{SourceURL: "http://example.com/ok.png", Filename: filename},
		{SourceURL: "http://example.com/evil.png", Filename: "../evil.png"},
		{SourceURL: "http://example.com/missing.png", Filename: "rss_missing.png"},
	})

	if len(got) != 1 {
		t.Fatalf("Adopt 返回 %d 个映射，期望 1: %v", len(got), got)
	}
	if got["http://example.com/ok.png"] != imageURLPrefix+filename {
		t.Errorf("Adopt URL = %q，期望 %q", got["http://example.com/ok.png"], imageURLPrefix+filename)
	}
}

// TestBackfillBatchRegistersLegacyRefs 确认升级前的历史缓存文件会被
// 补登资源记录与条目引用，从而不被清理当孤儿删除。
func TestBackfillBatchRegistersLegacyRefs(t *testing.T) {
	dir := t.TempDir()
	const filename = "rss_0123456789abcdef0123456789abcdef.png"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	// 丢失的文件不应被登记，否则会留下永远 404 的记录。
	const missing = "rss_fedcba9876543210fedcba9876543210.jpg"

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// GORM 在 PostgreSQL 上用 INSERT ... RETURNING "id"，所以是 Query 而非 Exec。
	mock.ExpectQuery(`INSERT INTO "homehub_rss_images"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery(`SELECT \* FROM "homehub_rss_images" WHERE filename IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at",
			"cache_key", "source_url", "filename", "content_type",
			"size_bytes", "sha256", "last_seen_at",
		}).AddRow(
			9, time.Now(), time.Now(), nil,
			"0123456789abcdef0123456789abcdef", "", filename, "image/png",
			3, "", time.Now(),
		))
	mock.ExpectQuery(`INSERT INTO "homehub_rss_image_refs"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	c.backfillBatch([]model.RSSEntry{
		{
			BaseModel: model.BaseModel{ID: 1},
			Link:      "http://example.com/legacy",
			Content:   `<p><img src="/static/uploads/rss_img/` + filename + `"><img src="/static/uploads/rss_img/` + missing + `"></p>`,
		},
	})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestReconcileRefsRemovesDanglingRefs 确认对账会删除没有对应条目的引用。
//
// 引用指向条目；条目被删而引用残留时，图片会被永久钉住、无法回收。
func TestReconcileRefsRemovesDanglingRefs(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`DELETE FROM "homehub_rss_image_refs" WHERE created_at <`).
		WillReturnResult(sqlmock.NewResult(0, 2))

	c := &LocalImageCache{db: db, dir: t.TempDir()}
	if err := c.reconcileRefs(context.Background()); err != nil {
		t.Fatalf("reconcileRefs 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestSweepSkipsPendingKeys 确认正在解析的缓存键不会被清理。
func TestSweepSkipsPendingKeys(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cleanup_enabled": true}`)

	dir := t.TempDir()
	filename := "rss_pending.png"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, filename), old, old); err != nil {
		t.Fatal(err)
	}

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "homehub_rss_images" WHERE last_seen_at <`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at",
			"cache_key", "source_url", "filename", "content_type",
			"size_bytes", "sha256", "last_seen_at",
		}).AddRow(
			5, old, old, nil,
			"pending", "", filename, "image/png",
			3, "", old,
		))
	// 被 pending 跳过，不会执行 DELETE。grace=0 走立即清理，
	// 历史文件扫描本来就不在本轮执行。

	c := &LocalImageCache{db: db, dir: dir, pending: map[string]struct{}{"pending": {}}}
	if err := c.Sweep(context.Background(), 0); err != nil {
		t.Fatalf("Sweep 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filename)); err != nil {
		t.Errorf("pending 中的文件不应被删除: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestFetchFeedDoesNotOverwriteExistingContent 确认重复抓取不会覆盖正文。
//
// 同一条内容可能被多个源收录：B 源抓到时正文已经由 A 源处理过。
// 再清洗一遍既浪费带宽下载图片，也可能用 B 的摘要覆盖 A 的全文。
func TestFetchFeedDoesNotOverwriteExistingContent(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": false, "retention_count": 5000}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "homehub_rss_feeds" WHERE "homehub_rss_feeds"."id" = \$1`).
		WithArgs(uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "title", "url",
			"site_url", "description", "icon_url", "fetch_interval", "last_fetch",
			"error_msg", "group_id",
		}).AddRow(7, now, now, nil, "B", "http://b/feed", "", "", "", 3600, nil, "", nil))

	mock.ExpectQuery(`INSERT INTO "homehub_rss_entries"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))

	// 该条目已存在且正文非空
	mock.ExpectQuery(`SELECT "id","link","content" FROM "homehub_rss_entries" WHERE link IN`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "link", "content"}).
			AddRow(11, "http://example.com/post", "<p>已有正文</p>"))

	// 关键：不应出现 UPDATE content
	mock.ExpectQuery(`INSERT INTO "homehub_rss_entry_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	mock.ExpectQuery(`SELECT count\(\*\) FROM "homehub_rss_entry_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectExec(`UPDATE "homehub_rss_feeds" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	images := &fakeImageCache{}
	s := &RSSService{db: db, images: images}
	s.SetFetcher(&fakeFetcher{})

	if err := s.FetchFeed(context.Background(), 7); err != nil {
		t.Fatalf("FetchFeed 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足（是否错误地更新了已有正文？）: %v", err)
	}
}

// TestPurgeOrphanEntriesIgnoresStillReferenced 确认孤儿判定基于真实关联，
// 而不是调用方给的候选集。
func TestPurgeOrphanEntriesIgnoresStillReferenced(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT "id" FROM "homehub_rss_entries" WHERE id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(12))
	mock.ExpectExec(`DELETE FROM "homehub_rss_entries" WHERE id IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM "homehub_rss_image_refs" WHERE entry_id IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	s := &RSSService{db: db}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.purgeOrphanEntries(tx, []uint64{11, 12})
	})
	if err != nil {
		t.Fatalf("purgeOrphanEntries 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestPurgeOrphanEntriesNoopOnEmpty 确认没有候选时不做任何查询。
func TestPurgeOrphanEntriesNoopOnEmpty(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	s := &RSSService{db: db}
	if err := s.purgeOrphanEntries(db, nil); err != nil {
		t.Fatalf("空候选应直接返回: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("不应执行任何 SQL: %v", err)
	}
}
