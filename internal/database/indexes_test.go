package database

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// newMockGorm 创建一个走 sqlmock 的 *gorm.DB，不连真实数据库。
func newMockGorm(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("创建 sqlmock 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("创建 gorm 实例失败: %v", err)
	}
	return db, mock
}

// TestEnsureSearchIndexes_NilDB 确认传入 nil 不会 panic。
func TestEnsureSearchIndexes_NilDB(t *testing.T) {
	EnsureSearchIndexes(nil)
}

// TestEnsureSearchIndexes_ExtensionUnavailable 确认建不了扩展时静默降级。
//
// 索引只是性能优化：数据库账号没有 CREATE EXTENSION 权限时，
// 服务必须照常启动，只是搜索走全表扫描。
func TestEnsureSearchIndexes_ExtensionUnavailable(t *testing.T) {
	db, mock := newMockGorm(t)

	mock.ExpectExec(`CREATE EXTENSION`).WillReturnError(errors.New("permission denied for database"))
	// 扩展失败后不应再尝试建索引，因此不设置 CREATE INDEX 的期望。

	EnsureSearchIndexes(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("调用序列不符（扩展失败后不应继续建索引）: %v", err)
	}
}

// TestEnsureSearchIndexes_CreatesAllIndexes 确认扩展可用时会建出全部索引。
func TestEnsureSearchIndexes_CreatesAllIndexes(t *testing.T) {
	db, mock := newMockGorm(t)

	mock.ExpectExec(`CREATE EXTENSION`).WillReturnResult(sqlmock.NewResult(0, 0))
	for range searchIndexes {
		mock.ExpectExec(`CREATE INDEX`).WillReturnResult(sqlmock.NewResult(0, 0))
	}

	EnsureSearchIndexes(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("未按预期建出全部索引: %v", err)
	}
}

// TestEnsureSearchIndexes_PartialFailure 确认单个索引失败不影响其余索引。
func TestEnsureSearchIndexes_PartialFailure(t *testing.T) {
	db, mock := newMockGorm(t)

	mock.ExpectExec(`CREATE EXTENSION`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE INDEX`).WillReturnError(errors.New("boom"))
	for i := 1; i < len(searchIndexes); i++ {
		mock.ExpectExec(`CREATE INDEX`).WillReturnResult(sqlmock.NewResult(0, 0))
	}

	EnsureSearchIndexes(db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("单个索引失败不应中断后续创建: %v", err)
	}
}

// TestSearchIndexesCoverSearchedColumns 锁住「搜索匹配哪些列，就在哪些列上建索引」。
//
// 搜索 SQL 里多一个 ILIKE 列而索引没跟上，那一列就是全表扫描；
// 反过来索引列表被误删，也只会表现为「搜索变慢」，很难归因。
func TestSearchIndexesCoverSearchedColumns(t *testing.T) {
	want := map[string]string{
		"idx_bookmarks_title_trgm":     "bookmarks USING GIN (title ",
		"idx_bookmarks_url_trgm":       "bookmarks USING GIN (url ",
		"idx_rss_entries_title_trgm":   "rss_entries USING GIN (title ",
		"idx_rss_entries_content_trgm": "rss_entries USING GIN (content ",
		"idx_rss_entries_author_trgm":  "rss_entries USING GIN (author ",
		"idx_todos_title_trgm":         "todos USING GIN (title ",
		"idx_todos_description_trgm":   "todos USING GIN (description ",
		"idx_log_entries_message_trgm": "log_entries USING GIN (message ",
		"idx_pastes_content_trgm":      "pastes USING GIN (content ",
	}
	got := map[string]string{}
	for _, idx := range searchIndexes {
		got[idx.name] = idx.sql
	}
	for name, frag := range want {
		sql, ok := got[name]
		if !ok {
			t.Errorf("缺少索引 %s", name)
			continue
		}
		if !strings.Contains(sql, frag) {
			t.Errorf("索引 %s 的目标不符，期望含 %q:\n\t%s", name, frag, sql)
		}
	}
}
