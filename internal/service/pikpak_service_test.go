package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// fakePikPak 是可控的 PikPakClient 替身。
type fakePikPak struct {
	submitted []string
	taskID    string
	err       error
}

func (f *fakePikPak) Submit(_ context.Context, magnet string) (string, error) {
	f.submitted = append(f.submitted, magnet)
	if f.err != nil {
		return "", f.err
	}
	taskID := f.taskID
	if taskID == "" {
		taskID = "task-123"
	}
	return taskID, nil
}

// rssEntryColumns 是 RSSEntry 查询所需的列。
func rssEntryColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "deleted_at",
		"title", "link", "author", "content", "magnet_url",
		"published", "fetched_at",
		"magnet_locked", "magnet_locked_at", "magnet_locked_by",
		"pikpak_status", "pikpak_task_id", "pikpak_error", "pikpak_saved_at",
	}
}

// TestLockMagnetSetsFields 确认锁定写入全部三个字段。
func TestLockMagnetSetsFields(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "rss_entries" SET .*magnet_locked`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &RSSService{db: db}
	operator := &model.User{BaseModel: model.BaseModel{ID: 7}, Level: model.LevelEditor}

	if err := s.LockMagnet(42, operator); err != nil {
		t.Fatalf("锁定失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestLockMagnetRejectsNilOperator 确认未认证不能锁定。
func TestLockMagnetRejectsNilOperator(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	s := &RSSService{db: db}
	if err := s.LockMagnet(1, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，得到: %v", err)
	}
}

// TestLockMagnetNotFound 确认条目不存在时返回明确错误。
func TestLockMagnetNotFound(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "rss_entries" SET .*magnet_locked`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	s := &RSSService{db: db}
	operator := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}

	if err := s.LockMagnet(999, operator); !errors.Is(err, ErrRSSEntryNotFound) {
		t.Fatalf("期望 ErrRSSEntryNotFound，得到: %v", err)
	}
}

// TestUnlockMagnetClearsFields 确认解锁清空标记但不碰 PikPak 状态。
//
// 已经提交到 PikPak 的任务是既成事实，解锁不代表撤销提交。
func TestUnlockMagnetClearsFields(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "rss_entries" SET .*magnet_locked`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &RSSService{db: db}
	if err := s.UnlockMagnet(42); err != nil {
		t.Fatalf("解锁失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestSubmitMagnetRequiresLock 确认未锁定不允许提交。
//
// 提交是外部副作用（往别人账号塞下载任务），不该对一条随时可能被
// 抓取改写的链接做。
func TestSubmitMagnetRequiresLock(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows(rssEntryColumns()).AddRow(
			1, nil, nil, nil, "t", "http://x/1", "", "", "magnet:?xt=urn:btih:abc",
			nil, nil, false, nil, 0, "", "", "", nil,
		))

	s := &RSSService{db: db, pikpak: &fakePikPak{}}
	if _, err := s.SubmitMagnetToPikPak(context.Background(), 1); !errors.Is(err, ErrMagnetNotLocked) {
		t.Fatalf("期望 ErrMagnetNotLocked，得到: %v", err)
	}
}

// TestSubmitMagnetRequiresMagnet 确认没有磁力链接时不提交。
func TestSubmitMagnetRequiresMagnet(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows(rssEntryColumns()).AddRow(
			1, nil, nil, nil, "t", "http://x/1", "", "", "",
			nil, nil, true, nil, 1, "", "", "", nil,
		))

	s := &RSSService{db: db, pikpak: &fakePikPak{}}
	if _, err := s.SubmitMagnetToPikPak(context.Background(), 1); !errors.Is(err, ErrNoMagnet) {
		t.Fatalf("期望 ErrNoMagnet，得到: %v", err)
	}
}

// TestSubmitMagnetDisabled 确认未启用时返回明确错误。
func TestSubmitMagnetDisabled(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	s := &RSSService{db: db, pikpak: nil}
	if _, err := s.SubmitMagnetToPikPak(context.Background(), 1); !errors.Is(err, ErrPikPakDisabled) {
		t.Fatalf("期望 ErrPikPakDisabled，得到: %v", err)
	}
}

// TestSubmitMagnetSuccess 确认成功后写入 saved 状态与 task id。
func TestSubmitMagnetSuccess(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	const magnet = "magnet:?xt=urn:btih:abcdef012345&dn=测试文件"
	mock.ExpectQuery(`SELECT \* FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows(rssEntryColumns()).AddRow(
			1, nil, nil, nil, "t", "http://x/1", "", "", magnet,
			nil, nil, true, nil, 1, "", "", "", nil,
		))
	mock.ExpectExec(`UPDATE "rss_entries" SET .*pikpak_status`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fake := &fakePikPak{taskID: "task-abc"}
	s := &RSSService{db: db, pikpak: fake}

	entry, err := s.SubmitMagnetToPikPak(context.Background(), 1)
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if len(fake.submitted) != 1 || fake.submitted[0] != magnet {
		t.Errorf("提交的磁力链接不符: %v", fake.submitted)
	}
	if entry.PikPakStatus != "saved" {
		t.Errorf("状态 = %q，期望 saved", entry.PikPakStatus)
	}
	if entry.PikPakTaskID != "task-abc" {
		t.Errorf("任务 ID = %q", entry.PikPakTaskID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestSubmitMagnetRecordsFailure 确认失败原因落到 pikpak_error。
func TestSubmitMagnetRecordsFailure(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows(rssEntryColumns()).AddRow(
			1, nil, nil, nil, "t", "http://x/1", "", "", "magnet:?xt=urn:btih:abc",
			nil, nil, true, nil, 1, "", "", "", nil,
		))
	mock.ExpectExec(`UPDATE "rss_entries" SET .*pikpak_status`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &RSSService{db: db, pikpak: &fakePikPak{err: errors.New("上游 500")}}

	entry, err := s.SubmitMagnetToPikPak(context.Background(), 1)
	if err == nil {
		t.Fatal("上游失败应返回错误")
	}
	if entry.PikPakStatus != "failed" {
		t.Errorf("状态 = %q，期望 failed", entry.PikPakStatus)
	}
	if !strings.Contains(entry.PikPakError, "上游 500") {
		t.Errorf("错误信息未记录: %q", entry.PikPakError)
	}
}

// TestMagnetCondition 验证筛选条件白名单。
func TestMagnetCondition(t *testing.T) {
	valid := []string{"", "has", "locked", "unlocked", "pikpak_saved", "pikpak_failed"}
	for _, m := range valid {
		if _, ok := magnetCondition(m); !ok {
			t.Errorf("magnetCondition(%q) 应有效", m)
		}
	}
	invalid := []string{"bogus", "locked OR 1=1", "'; DROP TABLE rss_entries; --"}
	for _, m := range invalid {
		if _, ok := magnetCondition(m); ok {
			t.Errorf("magnetCondition(%q) 应被拒绝（防注入）", m)
		}
	}
}

// TestMagnetName 验证从磁力链接提取任务名。
func TestMagnetName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"magnet:?xt=urn:btih:abcdef0123456789&dn=文件名", "文件名"},
		{"magnet:?dn=名字&xt=urn:btih:abc", "名字"},
		{"magnet:?xt=urn:btih:abcdef0123456789", "magnet-abcdef01"},
		{"magnet:?foo=bar", "magnet-task"},
	}
	for _, c := range cases {
		if got := magnetName(c.in); got != c.want {
			t.Errorf("magnetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
