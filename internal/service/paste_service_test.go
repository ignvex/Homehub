package service

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
	"gorm.io/gorm"
)

// TestPurgeExpiredIssuesRealDelete 确认过期清理产生物理 DELETE。
//
// Paste 嵌入 BaseModel 因而带 gorm.DeletedAt。若不加 Unscoped，
// GORM 会生成 UPDATE ... SET deleted_at，过期数据永不真正清理，
// 表会随使用持续膨胀。这里用 sqlmock 的期望来卡住语句类型：
// 只声明 DELETE 的期望，若实际发出 UPDATE 则 ExpectationsWereMet 失败。
func TestPurgeExpiredIssuesRealDelete(t *testing.T) {
	loadConfigWith(t, `,"pastebin": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := NewPasteService(db)

	// testutil 的 gorm 实例启用了 SkipDefaultTransaction，故无 Begin/Commit
	mock.ExpectExec(`DELETE FROM "pastes" WHERE expires_at <= \$1`).
		WillReturnResult(sqlmock.NewResult(0, 3))

	n, err := svc.PurgeExpired()
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if n != 3 {
		t.Errorf("删除行数 = %d，期望 3", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符合预期（应为物理 DELETE）: %v", err)
	}
}

// TestDeletePasteIssuesRealDelete 确认按公开 ID 删除也是物理删除。
func TestDeletePasteIssuesRealDelete(t *testing.T) {
	loadConfigWith(t, `,"pastebin": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := NewPasteService(db)

	mock.ExpectExec(`DELETE FROM "pastes" WHERE public_id = \$1 AND delete_token = \$2`).
		WithArgs("abc123", "tok").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := svc.DeletePaste("abc123", "tok"); err != nil {
		t.Fatalf("DeletePaste: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符合预期（应为物理 DELETE）: %v", err)
	}
}

// TestPasteTableName 确认表名与建表 DDL 一致。
func TestPasteTableName(t *testing.T) {
	if got := (model.Paste{}).TableName(); got != "pastes" {
		t.Errorf("Paste.TableName() = %q，期望 pastes", got)
	}
}

// TestDeletePasteRequiresToken 确认删除必须提供正确的密钥。
//
// public_id 会出现在 URL 与粘贴列表中，单凭它不能证明请求者是创建者。
// 历史上 delete_token 生成了却从不校验，等于任何人都能删除别人的粘贴。
func TestDeletePasteRequiresToken(t *testing.T) {
	loadConfigWith(t, `,"pastebin": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewPasteService(db)

	if err := svc.DeletePaste("abc123", ""); !errors.Is(err, ErrPasteTokenMismatch) {
		t.Errorf("空密钥应返回 ErrPasteTokenMismatch，实际 %v", err)
	}

	// 密钥错误时 SQL 匹配不到行 → 视为不存在
	mock.ExpectExec(`DELETE FROM "pastes" WHERE public_id = \$1 AND delete_token = \$2`).
		WithArgs("abc123", "wrong").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := svc.DeletePaste("abc123", "wrong"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("密钥不匹配应返回 ErrRecordNotFound，实际 %v", err)
	}
}

// TestDeletePasteCorrectToken 确认正确密钥可以删除。
func TestDeletePasteCorrectToken(t *testing.T) {
	loadConfigWith(t, `,"pastebin": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewPasteService(db)

	mock.ExpectExec(`DELETE FROM "pastes" WHERE public_id = \$1 AND delete_token = \$2`).
		WithArgs("abc123", "right").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := svc.DeletePaste("abc123", "right"); err != nil {
		t.Errorf("正确密钥删除失败: %v", err)
	}
}
