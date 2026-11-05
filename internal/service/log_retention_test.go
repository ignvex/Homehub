package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestLogPurgeOlderThanIssuesHardDelete 锁住保留期清理发出的 SQL。
//
// 两件事都要证明：按 log_time 截断（而不是 created_at，后者在批量
// 写入时会晚于事件时间）；以及是物理 DELETE——走 GORM 默认的软删除
// 只会把行标记一下，表一行都不会变小。
func TestLogPurgeOlderThanIssuesHardDelete(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectExec(`^DELETE FROM "log_entries" WHERE log_time < \$1$`).
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 42))

	n, err := NewLogService(db).PurgeOlderThan(cutoff)
	if err != nil {
		t.Fatalf("PurgeOlderThan 失败: %v", err)
	}
	if n != 42 {
		t.Fatalf("删除行数 = %d，期望 42", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// TestLogRetentionLoopRunsAtStartup 确认启动时立即跑一轮清理，不必等 6 小时，
// 且保留期读自当前配置。
func TestLogRetentionLoopRunsAtStartup(t *testing.T) {
	loadConfigWith(t, `,"log": {"level": "info", "retention_days": 7}`)
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`DELETE FROM "log_entries" WHERE log_time < \$1`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	done := make(chan struct{})
	svc := NewLogService(db)
	svc.afterPurge = func() { close(done) }

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	svc.StartRetentionLoop(ctx, &wg)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("启动后应立即执行一轮清理")
	}
	cancel()
	wg.Wait()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// TestLogRetentionCutoff 锁住保留期到截止时间的换算，尤其是 0 = 不清理。
//
// 0 被当成「保留 0 天」的话，每一轮都会把整张日志表删光。
func TestLogRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	if _, ok := logRetentionCutoff(0, now); ok {
		t.Fatal("retention_days=0 表示不限，不应清理")
	}
	if _, ok := logRetentionCutoff(-5, now); ok {
		t.Fatal("非法负值不应触发清理")
	}
	got, ok := logRetentionCutoff(7, now)
	if !ok || !got.Equal(now.AddDate(0, 0, -7)) {
		t.Fatalf("7 天的截止时间 = %v (ok=%v)，期望 %v", got, ok, now.AddDate(0, 0, -7))
	}
}
