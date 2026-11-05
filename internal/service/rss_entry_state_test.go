package service

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

var entryListCols = []string{
	"id", "title", "link", "author", "magnet_url", "published", "fetched_at",
	"magnet_locked", "magnet_locked_at", "pikpak_status", "pikpak_task_id",
	"pikpak_error", "pikpak_saved_at", "content_head", "has_content",
	"is_read", "is_starred",
}

// 已读/星标必须在取详情的那一条 SQL 里 LEFT JOIN 带出，并按当前用户过滤。
// 防的是两种回归：逐条再查状态（N+1，sqlmock 会报多余查询），以及
// JOIN 条件漏了 user_id——那样别人的已读会显示在你的列表上。
func TestListEntriesJoinsCurrentUserStateInDetailQuery(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT COUNT`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT e.id FROM`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7).AddRow(8))
	mock.ExpectQuery(`(?s)SELECT.*\(st.read_at IS NOT NULL\) AS is_read.*\(st.starred_at IS NOT NULL\) AS is_starred.*`+
		`LEFT JOIN rss_entry_states AS st ON st.entry_id = rss_entries.id AND st.user_id = \$1.*rss_entries.id IN`).
		WithArgs(42, 7, 8).
		WillReturnRows(sqlmock.NewRows(entryListCols).
			AddRow(7, "a", "https://x/7", "", "", now, now, false, nil, "", "", "", nil, "", false, true, false).
			AddRow(8, "b", "https://x/8", "", "", now, now, false, nil, "", "", "", nil, "", false, false, true))
	mock.ExpectQuery(`(?s)SELECT.*rss_entry_feeds`).
		WillReturnRows(sqlmock.NewRows([]string{"entry_id", "feed_id"}).AddRow(7, 3).AddRow(8, 3))

	svc := &RSSService{db: db}
	views, _, err := svc.ListEntries(EntryFilter{Page: 1, PerPage: 20, UserID: 42})
	if err != nil {
		t.Fatalf("ListEntries 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
	if !views[0].IsRead || views[0].IsStarred {
		t.Errorf("7 号应为已读、未星标: %+v", views[0])
	}
	if views[1].IsRead || !views[1].IsStarred {
		t.Errorf("8 号应为未读、已星标: %+v", views[1])
	}
}

// state 筛选要落到计数与分页两条查询上，否则 total 与列表对不上，
// 前端翻页会翻出空页。
func TestListEntriesStateFilterAppliesToCountAndPage(t *testing.T) {
	cases := map[string]string{
		"unread":  `st.read_at IS NULL`,
		"read":    `st.read_at IS NOT NULL`,
		"starred": `st.starred_at IS NOT NULL`,
	}
	for state, cond := range cases {
		t.Run(state, func(t *testing.T) {
			db, mock, cleanup := testutil.NewMockDB(t)
			defer cleanup()

			join := `LEFT JOIN rss_entry_states AS st ON st.entry_id = e.id AND st.user_id = \$1`
			mock.ExpectQuery(`(?s)SELECT COUNT.*` + join + `.*` + cond).
				WithArgs(42).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectQuery(`(?s)SELECT e.id FROM.*` + join + `.*` + cond).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))

			svc := &RSSService{db: db}
			if _, _, err := svc.ListEntries(EntryFilter{Page: 1, PerPage: 20, UserID: 42, State: state}); err != nil {
				t.Fatalf("ListEntries 失败: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("SQL 期望未满足: %v", err)
			}
		})
	}
}

// 非法 state 必须报错而不是忽略：忽略等于返回全部条目，「只看未读」
// 拼错一个字母就会把已读的全显示出来。
func TestListEntriesBadStateIsSentinel(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := &RSSService{db: db}
	_, _, err := svc.ListEntries(EntryFilter{UserID: 1, State: "unred"})
	if !errors.Is(err, ErrRSSBadStateFilter) {
		t.Fatalf("期望 ErrRSSBadStateFilter，got %v", err)
	}
}

// 标记已读走 INSERT ... SELECT FROM rss_entries ... ON CONFLICT，
// 条目不存在时 RowsAffected=0 → 404，而不是插入一条悬挂状态行。
func TestSetEntryReadUpsertsViaSelectFromEntries(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`(?s)INSERT INTO rss_entry_states \(user_id, entry_id, read_at, updated_at\)\s+SELECT .* FROM rss_entries e\s+WHERE e.id = \$4 AND e.deleted_at IS NULL\s+ON CONFLICT \(user_id, entry_id\) DO UPDATE\s+SET read_at = COALESCE\(rss_entry_states.read_at, EXCLUDED.read_at\)`).
		WithArgs(5, sqlmock.AnyArg(), sqlmock.AnyArg(), 9).
		WillReturnResult(sqlmock.NewResult(0, 0))

	svc := &RSSService{db: db}
	if err := svc.SetEntryRead(5, 9, true); !errors.Is(err, ErrRSSEntryNotFound) {
		t.Fatalf("条目不存在时应返回 ErrRSSEntryNotFound，got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 取消星标后若已读也为空，这一行应被删除，避免表里堆积全空行。
func TestUnstarClearsColumnAndDropsEmptyRow(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE rss_entry_states SET starred_at = NULL, updated_at = \$1\s+WHERE user_id = \$2 AND entry_id = \$3`).
		WithArgs(sqlmock.AnyArg(), 5, 9).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM rss_entry_states\s+WHERE user_id = \$1 AND entry_id = \$2 AND read_at IS NULL AND starred_at IS NULL`).
		WithArgs(5, 9).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	svc := &RSSService{db: db}
	if err := svc.SetEntryStarred(5, 9, false); err != nil {
		t.Fatalf("取消星标失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 用户 ID 为 0（未登录、上下文丢失）不得写入任何状态行。
func TestEntryStateRequiresUser(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := &RSSService{db: db}
	if err := svc.SetEntryRead(0, 1, true); !errors.Is(err, ErrForbidden) {
		t.Errorf("SetEntryRead(0) = %v", err)
	}
	if _, err := svc.MarkEntriesRead(0, MarkReadInput{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("MarkEntriesRead(0) = %v", err)
	}
	if _, err := svc.UnreadCounts(0); !errors.Is(err, ErrForbidden) {
		t.Errorf("UnreadCounts(0) = %v", err)
	}
}

// 批量已读是一条 INSERT ... SELECT ... ON CONFLICT DO UPDATE WHERE read_at IS NULL：
// 已读的行不重写，返回值就是「这次新标记的条数」。按源/分组/时间的条件
// 都要进同一条语句。
func TestMarkEntriesReadIsSingleUpsert(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	feed, group := uint64(3), uint64(4)
	before := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectExec(`(?s)INSERT INTO rss_entry_states \(user_id, entry_id, read_at, updated_at\)\s+SELECT .*FROM rss_entries e\s+WHERE e.deleted_at IS NULL AND EXISTS \(SELECT 1 FROM rss_entry_feeds ef JOIN rss_feeds f ON f.id = ef.feed_id AND f.deleted_at IS NULL WHERE ef.entry_id = e.id AND ef.deleted_at IS NULL AND ef.feed_id = \$4 AND f.group_id = \$5\) AND e.id IN \(\$6,\$7\) AND e.published <= \$8.*`+
		`ON CONFLICT \(user_id, entry_id\) DO UPDATE.*WHERE rss_entry_states.read_at IS NULL`).
		WithArgs(5, sqlmock.AnyArg(), sqlmock.AnyArg(), feed, group, 10, 11, before).
		WillReturnResult(sqlmock.NewResult(0, 2))

	svc := &RSSService{db: db}
	n, err := svc.MarkEntriesRead(5, MarkReadInput{FeedID: &feed, GroupID: &group, EntryIDs: []uint64{10, 11}, Before: &before})
	if err != nil {
		t.Fatalf("MarkEntriesRead 失败: %v", err)
	}
	if n != 2 {
		t.Errorf("affected = %d, want 2", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// entry_ids 显式给了空数组必须拒绝：否则它会退化成「无 id 限定」，
// 一次误传就把全部条目标成已读。
func TestMarkEntriesReadRejectsEmptyIDList(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := &RSSService{db: db}
	if _, err := svc.MarkEntriesRead(5, MarkReadInput{EntryIDs: []uint64{}}); !errors.Is(err, ErrMarkReadEmptyIDs) {
		t.Fatalf("期望 ErrMarkReadEmptyIDs，got %v", err)
	}
}

// 未读计数一条 SQL：GROUPING SETS 同时给出总数、按源、按分组。
// GROUPING() 区分「分组汇总行」与「group_id 本身为 NULL 的未分组源」，
// 后者不得出现在 by_group 里。
func TestUnreadCountsSingleGroupingSetsQuery(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`(?s)SELECT ef.feed_id, f.group_id.*COUNT\(DISTINCT ef.entry_id\).*FROM rss_entry_feeds ef.*` +
		`LEFT JOIN rss_entry_states st ON st.entry_id = ef.entry_id AND st.user_id = \$1.*st.read_at IS NULL.*` +
		`GROUP BY GROUPING SETS \(\(ef.feed_id\), \(f.group_id\), \(\)\)`).
		WithArgs(5).
		WillReturnRows(sqlmock.NewRows([]string{"feed_id", "group_id", "g_feed", "g_group", "n"}).
			AddRow(3, nil, 0, 1, 4).
			AddRow(6, nil, 0, 1, 2).
			AddRow(nil, 9, 1, 0, 4).
			AddRow(nil, nil, 1, 0, 2). // 未分组源的汇总：不进 by_group
			AddRow(nil, nil, 1, 1, 5))

	svc := &RSSService{db: db}
	got, err := svc.UnreadCounts(5)
	if err != nil {
		t.Fatalf("UnreadCounts 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
	if got.Total != 5 || got.ByFeed[3] != 4 || got.ByFeed[6] != 2 || got.ByGroup[9] != 4 || len(got.ByGroup) != 1 {
		t.Errorf("计数不符: %+v", got)
	}
}

// 删除用户时，阅读状态与待办归属要在同一事务里清掉。没有外键，漏删就永久残留，
// 而且新用户若复用了 id（序列重置后），会继承别人的已读记录。
func TestDeleteUserRemovesEntryStatesAndTodoRefs(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	target := &model.User{BaseModel: model.BaseModel{ID: 9}, Username: "bob", Level: model.LevelMember, IsActive: true}
	mock.ExpectQuery(`SELECT \* FROM "users"`).
		WillReturnRows(sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(target)...))
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "sessions" WHERE user_id = \$1`).WithArgs(9).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM "avatar_caches" WHERE user_id = \$1`).WithArgs(9).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM "rss_entry_states" WHERE user_id = \$1`).WithArgs(9).WillReturnResult(sqlmock.NewResult(0, 3))
	// 待办的归属引用置 NULL（同一事务）。
	mock.ExpectExec(`UPDATE "todos" SET "created_by"=\$1,"updated_at"=\$2 WHERE created_by = \$3`).WithArgs(nil, sqlmock.AnyArg(), 9).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE "todos" SET "assignee_id"=\$1,"updated_at"=\$2 WHERE assignee_id = \$3`).WithArgs(nil, sqlmock.AnyArg(), 9).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "users" SET "deleted_at"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	operator := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}
	if err := NewAuthService(db).DeleteUser(9, operator); err != nil {
		t.Fatalf("DeleteUser 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}
