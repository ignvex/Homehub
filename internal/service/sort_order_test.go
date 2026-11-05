package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 拖动排序曾是「每个 ID 一条 UPDATE」：50 个书签就是 50 次往返，
// 中途失败时靠事务兜底。改成一条 CASE 语句后，排序要么整体生效
// 要么整体不变，也不需要事务。这些用例锁住发出去的 SQL 形状。

const reorderSQL = `UPDATE %s SET sort_order = CASE id WHEN \$1::bigint THEN \$2::bigint WHEN \$3::bigint THEN \$4::bigint WHEN \$5::bigint THEN \$6::bigint END, updated_at = \$7 WHERE id IN \(\$8,\$9,\$10\) AND deleted_at IS NULL`

func TestReorderTodosIssuesSingleCaseUpdate(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`^`+sprintfRe(reorderSQL, "todos")+`$`).
		WithArgs(30, 1, 10, 2, 20, 3, sqlmock.AnyArg(), 30, 10, 20).
		WillReturnResult(sqlmock.NewResult(0, 3))

	if err := NewTodoService(db).ReorderTodos(editor, []uint64{30, 10, 20}); err != nil {
		t.Fatalf("ReorderTodos 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

func TestReorderGroupsIssuesSingleCaseUpdate(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`^`+sprintfRe(reorderSQL, "bookmark_groups")+`$`).
		WithArgs(3, 1, 1, 2, 2, 3, sqlmock.AnyArg(), 3, 1, 2).
		WillReturnResult(sqlmock.NewResult(0, 3))

	if err := NewBookmarkService(db).ReorderGroups([]uint64{3, 1, 2}); err != nil {
		t.Fatalf("ReorderGroups 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 书签排序必须限定在分组内：否则把别的分组的书签 ID 塞进来就能改它们的顺序。
func TestReorderBookmarksIsScopedToGroup(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`^UPDATE bookmarks SET sort_order = CASE id WHEN \$1::bigint THEN \$2::bigint WHEN \$3::bigint THEN \$4::bigint END, updated_at = \$5 WHERE id IN \(\$6,\$7\) AND deleted_at IS NULL AND group_id = \$8$`).
		WithArgs(5, 1, 4, 2, sqlmock.AnyArg(), 5, 4, 9).
		WillReturnResult(sqlmock.NewResult(0, 2))

	if err := NewBookmarkService(db).ReorderBookmarks(9, []uint64{5, 4}); err != nil {
		t.Fatalf("ReorderBookmarks 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 空列表不发任何 SQL：`WHERE id IN ()` 是语法错误。
func TestReorderEmptyIsNoop(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	if err := NewTodoService(db).ReorderTodos(editor, nil); err != nil {
		t.Fatalf("空列表应直接返回: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 查最大排序值失败时，旧代码忽略错误、按 0 继续，新条目就排到最前面；
// 数据库已经出错时继续写入也没有意义。
func TestCreateTodoFailsWhenMaxSortOrderQueryFails(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`MAX\(sort_order\)`).WillReturnError(errors.New("boom"))

	if _, err := NewTodoService(db).CreateTodo(editor, CreateTodoInput{Title: "t"}); err == nil {
		t.Fatal("查询排序值失败时应返回错误")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

func TestCreateGroupFailsWhenMaxSortOrderQueryFails(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`MAX\(sort_order\)`).WillReturnError(errors.New("boom"))

	if _, err := NewBookmarkService(db).CreateGroup("g"); err == nil {
		t.Fatal("查询排序值失败时应返回错误")
	}
}

func TestCreateBookmarkFailsWhenMaxSortOrderQueryFails(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "bookmark_groups"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`MAX\(sort_order\)`).WillReturnError(errors.New("boom"))

	if _, err := NewBookmarkService(db).CreateBookmark(1, "t", "https://example.com", ""); err == nil {
		t.Fatal("查询排序值失败时应返回错误")
	}
}

// sprintfRe 把表名填进正则模板。
func sprintfRe(tmpl, table string) string {
	return fmt.Sprintf(tmpl, table)
}
