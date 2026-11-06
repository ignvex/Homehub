package service

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestUpdateBookmarkRejectsDangerousURL 防的是存储型 XSS：创建书签会拒绝
// javascript: 等协议，但更新曾经不校验，把已有书签改成 javascript:alert(1)
// 后，首页与书签页的 <a href> 点一下就执行。断言不发任何 UPDATE。
func TestUpdateBookmarkRejectsDangerousURL(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,<script>", "file:///etc/passwd"} {
		db, mock, cleanup := testutil.NewMockDB(t)
		err := NewBookmarkService(db).UpdateBookmark(1, BookmarkUpdate{URL: &bad})
		if err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("%q: %v", bad, err)
		}
		cleanup()
	}
}

// TestUpdateBookmarkMovesToAnotherGroup 让编辑弹窗里的「分组」真正可用：
// 旧接口没有 group_id，只能删了重建。移动时追加到目标分组末尾，否则会与
// 目标分组里已有的 sort_order 撞号，排序按钮表现为「点了没反应」。
func TestUpdateBookmarkMovesToAnotherGroup(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "bookmark_groups" WHERE id = \$1`).
		WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(sort_order\), 0\) FROM "bookmarks" WHERE group_id = \$1`).
		WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(7))
	mock.ExpectExec(`UPDATE "bookmarks" SET "group_id"=\$1,"sort_order"=\$2,"updated_at"=\$3 WHERE`).
		WithArgs(2, 8, sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))

	group := uint64(2)
	if err := NewBookmarkService(db).UpdateBookmark(1, BookmarkUpdate{GroupID: &group}); err != nil {
		t.Fatalf("移动分组失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateBookmarkMissingGroupIsNotFound 移到不存在的分组要报 ErrGroupNotFound，
// 不能写出一条指向空分组、在任何列表里都看不到的「幽灵书签」。
func TestUpdateBookmarkMissingGroupIsNotFound(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	mock.ExpectQuery(`SELECT count\(\*\) FROM "bookmark_groups"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	group := uint64(99)
	if err := NewBookmarkService(db).UpdateBookmark(1, BookmarkUpdate{GroupID: &group}); err != ErrGroupNotFound {
		t.Fatalf("期望 ErrGroupNotFound，得到 %v", err)
	}
}
