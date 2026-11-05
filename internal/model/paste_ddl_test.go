package model_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestCreatePastesTableIndexesExpiresAt 防的是过期清理退化成全表扫描。
//
// 列表（expires_at > now）与清理循环（expires_at <= now）都按这一列过滤；
// pastes 是匿名可写的表，没有索引时行数增长会直接拖慢每一次清理。
func TestCreatePastesTableIndexesExpiresAt(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS pastes`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_pastes_expires_at ON pastes \(expires_at\)`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := model.CreatePastesTable(db); err != nil {
		t.Fatalf("CreatePastesTable 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}
