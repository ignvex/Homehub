package service

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 批量更新拼的是 `WHERE id IN ?`。如果空列表被放行，GORM 生成的
// 条件会退化，一次请求就能把整张 rss_feeds 表改掉——用户的几十个
// 订阅源会同时变成本机抓取或某个分组，而且没有任何报错。
// 这条断言要求空选中在碰到数据库之前就被拒掉。
func TestUpdateFeedsRejectsEmptySelection(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := &RSSService{db: db}
	interval := 600
	for _, ids := range [][]uint64{nil, {}, {0}, {0, 0}} {
		if _, err := svc.UpdateFeeds(ids, FeedUpdate{FetchInterval: &interval}); err != ErrRSSBatchEmpty {
			t.Fatalf("ids=%v 期望 ErrRSSBatchEmpty，got %v", ids, err)
		}
	}
	// 一条 SQL 都不该发出：没有 ExpectExec，任何查询都会让 sqlmock 报错。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("空选中不应访问数据库: %v", err)
	}
}

// 批量写必须是一条 IN 语句，而不是逐条更新。
// 断言 SQL 形态而不是最终内存状态：真正的风险是「漏掉了 IN 条件」，
// 那种失守不会体现在返回值上。
func TestUpdateFeedsIssuesSingleUpdateWithIDList(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	// GORM 自动补 updated_at 与软删除条件：两者都必须保留，
	// 否则已删除的源会被一次批量操作「复活」。
	mock.ExpectExec(`UPDATE "rss_feeds" SET "fetch_enabled"=\$1,"updated_at"=\$2 WHERE id IN \(\$3,\$4\) AND .*deleted_at.* IS NULL`).
		WithArgs(false, sqlmock.AnyArg(), uint64(3), uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	svc := &RSSService{db: db}
	paused := false
	affected, err := svc.UpdateFeeds([]uint64{3, 9}, FeedUpdate{FetchEnabled: &paused})
	if err != nil {
		t.Fatalf("批量更新失败: %v", err)
	}
	if affected != 2 {
		t.Fatalf("期望更新 2 条，got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// 重复 ID 不能把 RowsAffected 计成两倍——界面上的「已更新 N 个」
// 会因此大于实际选中的数量。
func TestUpdateFeedsDeduplicatesIDs(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "rss_feeds" SET .*WHERE id IN \(\$3,\$4\) AND .*deleted_at.* IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	svc := &RSSService{db: db}
	affected, err := svc.UpdateFeeds([]uint64{5, 5, 7}, FeedUpdate{FetchEnabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("批量更新失败: %v", err)
	}
	if affected != 2 {
		t.Fatalf("重复 ID 未去重：期望 2，got %d", affected)
	}
}

// 校验失败必须在事务之外拦下：间隔填 0 时不该已经开始一次写入。
func TestUpdateFeedsValidatesBeforeWriting(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := &RSSService{db: db}
	zero := 0
	if _, err := svc.UpdateFeeds([]uint64{1, 2}, FeedUpdate{FetchInterval: &zero}); err == nil {
		t.Fatal("间隔 0 应被拒绝")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("校验失败不应开始事务: %v", err)
	}
}

// 没有任何字段要改时，不该发出一条空的 UPDATE。
func TestUpdateFeedsNoOpForEmptyPatch(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := &RSSService{db: db}
	affected, err := svc.UpdateFeeds([]uint64{1, 2}, FeedUpdate{})
	if err != nil {
		t.Fatalf("空变更不应报错: %v", err)
	}
	if affected != 0 {
		t.Fatalf("空变更不该影响任何行，got %d", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("空变更不应访问数据库: %v", err)
	}
}

// 数量上限防止一次拼错的 ID 列表变成超长 IN。
func TestUpdateFeedsRejectsOversizedBatch(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	ids := make([]uint64, maxBatchFeeds+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	svc := &RSSService{db: db}
	if _, err := svc.UpdateFeeds(ids, FeedUpdate{FetchEnabled: boolPtr(true)}); err == nil {
		t.Fatal("超出上限应被拒绝")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("超限请求不应访问数据库: %v", err)
	}
}

// 清空分组必须写成 NULL 而不是 0：0 不是合法分组 ID，
// 写进去会让这些源在按分组筛选时彻底消失。
func TestUpdateFeedsClearsGroupWithNull(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	// 参数必须是 nil（SQL 里渲染成 NULL），不能是 0——
	// 0 不是合法分组 ID，写进去会让这些源按分组筛选时彻底消失。
	mock.ExpectExec(`UPDATE "rss_feeds" SET "group_id"=\$1,"updated_at"=\$2 WHERE id IN \(\$3,\$4\)`).
		WithArgs(nil, sqlmock.AnyArg(), uint64(1), uint64(2)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	svc := &RSSService{db: db}
	if _, err := svc.UpdateFeeds([]uint64{1, 2}, FeedUpdate{Group: GroupUpdate{Set: true, Value: nil}}); err != nil {
		t.Fatalf("清空分组失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

func boolPtr(v bool) *bool { return &v }
