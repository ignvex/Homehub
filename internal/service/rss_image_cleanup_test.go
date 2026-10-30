package service

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// fakeImageCache 是 RSSImageCache 的测试替身，只记录 Sweep 调用。
type fakeImageCache struct {
	sweeps []time.Duration
}

func (f *fakeImageCache) Cache(context.Context, uint64, string, []string) map[string]string {
	return map[string]string{}
}

func (f *fakeImageCache) Sweep(_ context.Context, grace time.Duration) error {
	f.sweeps = append(f.sweeps, grace)
	return nil
}

// TestDeleteFeedPhysicallyRemovesRowsAndSweeps 确认删除订阅源时：
//   - 条目与图片引用是物理删除（不是软删除留下垃圾行）
//   - 删除完成后触发一次立即回收（grace=0）
func TestDeleteFeedPhysicallyRemovesRowsAndSweeps(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "homehub_rss_entries" WHERE feed_id = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`DELETE FROM "homehub_rss_image_refs" WHERE feed_id = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`DELETE FROM "homehub_rss_feeds" WHERE "homehub_rss_feeds"."id" = \$1`).
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

// TestEnforceRetentionDeletesOldEntriesAndRefs 确认保留策略：
//   - 超出 retention_count 的最旧条目被物理删除
//   - 对应条目的图片引用同步删除
//   - 随后触发立即回收
func TestEnforceRetentionDeletesOldEntriesAndRefs(t *testing.T) {
	loadConfigWith(t, `,"rss": {"retention_count": 2}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "homehub_rss_entries" WHERE feed_id = $1 AND "homehub_rss_entries"."deleted_at" IS NULL`)).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "id","link" FROM "homehub_rss_entries" WHERE feed_id = $1 AND "homehub_rss_entries"."deleted_at" IS NULL ORDER BY published ASC, id ASC LIMIT $2`)).
		WithArgs(uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "link"}).AddRow(11, "http://example.com/old"))

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "homehub_rss_entries" WHERE id IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM "homehub_rss_image_refs" WHERE feed_id = .+ AND entry_hash IN`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	images := &fakeImageCache{}
	s := &RSSService{db: db, images: images}

	if err := s.enforceRetention(context.Background(), 7); err != nil {
		t.Fatalf("enforceRetention 失败: %v", err)
	}
	if len(images.sweeps) != 1 || images.sweeps[0] != 0 {
		t.Errorf("Sweep 调用 = %v，期望一次 grace=0", images.sweeps)
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

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "homehub_rss_entries"`)).
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

// TestSweepDisabledConfigIsNoop 确认关闭自动清理后 Sweep 不产生任何 SQL。
func TestSweepDisabledConfigIsNoop(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cleanup_enabled": false}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	c := &LocalImageCache{db: db, dir: t.TempDir()}
	if err := c.Sweep(context.Background(), 0); err != nil {
		t.Fatalf("Sweep 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("关闭清理后不应执行 SQL: %v", err)
	}
}

// TestSweepWithoutDBIsNoop 确认没有数据库时（单测/独立 worker）
// 不会误删缓存目录里的文件。
func TestSweepWithoutDBIsNoop(t *testing.T) {
	c := &LocalImageCache{db: nil, dir: t.TempDir()}
	if err := c.Sweep(context.Background(), 0); err != nil {
		t.Fatalf("Sweep 失败: %v", err)
	}
}

// TestRSSImageTableNames 确认新表遵循 homehub_ 前缀约定。
func TestRSSImageTableNames(t *testing.T) {
	if got := (model.RSSImage{}).TableName(); got != "homehub_rss_images" {
		t.Errorf("RSSImage 表名 = %q", got)
	}
	if got := (model.RSSImageRef{}).TableName(); got != "homehub_rss_image_refs" {
		t.Errorf("RSSImageRef 表名 = %q", got)
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

	mock.ExpectExec(`DELETE FROM "homehub_rss_image_refs" WHERE feed_id NOT IN`).
		WillReturnResult(sqlmock.NewResult(0, 0))

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
	got := c.Adopt(1, "http://example.com/item", []AdoptedImage{
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
			FeedID:    7,
			Link:      "http://example.com/legacy",
			Content:   `<p><img src="/static/uploads/rss_img/` + filename + `"><img src="/static/uploads/rss_img/` + missing + `"></p>`,
		},
	})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}
