package service

import (
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ignvex/HomeHub/internal/testutil"
)

// tooManyReorderIDs 返回比上限多一个的 id 列表。
func tooManyReorderIDs() []uint64 {
	ids := make([]uint64, maxReorderIDs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	return ids
}

// TestReorderRejectsTooManyIDs 防的是超长 id 列表把排序接口打成 500。
//
// reorderBySortOrder 每个 id 占 3 个绑定参数，约 3.2 万个 id 就超过
// PostgreSQL 单条语句 65535 个参数的上限，数据库报错后接口返回 500。
// 上限在 service 层拦下，返回哨兵错误由 handler 映射为 400，且不发任何 SQL
// （待办的归属校验查询同样带着整份 id 列表）。
func TestReorderRejectsTooManyIDs(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	ids := tooManyReorderIDs()
	bm := NewBookmarkService(db)
	cases := map[string]func() error{
		"groups":       func() error { return bm.ReorderGroups(ids) },
		"bookmarks":    func() error { return bm.ReorderBookmarks(1, ids) },
		"todos_editor": func() error { return NewTodoService(db).ReorderTodos(editor, ids) },
		"todos_member": func() error { return NewTodoService(db).ReorderTodos(member, ids) },
	}
	for name, call := range cases {
		if err := call(); !errors.Is(err, ErrReorderTooManyIDs) {
			t.Errorf("%s: 期望 ErrReorderTooManyIDs，got %v", name, err)
		}
	}
	// 没有设任何 SQL 期望：若有语句发出，sqlmock 会报未预期调用。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// TestReorderAcceptsExactlyMaxIDs 确认上限本身可用：恰好 maxReorderIDs 个
// id 仍会发出 UPDATE。
func TestReorderAcceptsExactlyMaxIDs(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	ids := tooManyReorderIDs()[:maxReorderIDs]
	mock.ExpectExec(`UPDATE bookmark_groups SET sort_order = CASE id`).
		WillReturnResult(sqlmockResult(int64(len(ids))))
	if err := NewBookmarkService(db).ReorderGroups(ids); err != nil {
		t.Fatalf("恰好 %d 个 id 不应失败: %v", maxReorderIDs, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

func sqlmockResult(n int64) driver.Result { return sqlmock.NewResult(0, n) }
