package service

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// adminSelfRows 返回 ID=1 的 1 级管理员。
func adminSelfRows(t *testing.T, email string) *sqlmock.Rows {
	t.Helper()
	u := testutil.CreateTestUser(t, "admin", "admin", email)
	return sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(u)...)
}

// TestAdminCanSetOwnEmailWhenFormEchoesLevelAndStatus 防的是「管理员设置不了
// 自己的邮箱」。编辑表单总会把当前的 level 与 is_active 原样回传；旧实现只要
// 这两个字段非空就判定为「改自己的等级/状态」并返回 ErrSelfOperation，连带
// 邮箱也写不进去。值未变化时应当放行，并且 SQL 里只更新 email。
func TestAdminCanSetOwnEmailWhenFormEchoesLevelAndStatus(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	operator := testutil.CreateTestUser(t, "admin", "admin", "")
	mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, ""))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE \(email = \$1 AND id != \$2\)`).
		WithArgs("admin@example.com", 1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// 断言 SET 子句只有 email + updated_at：level / is_active 不能被顺手写入。
	mock.ExpectExec(`UPDATE "users" SET "email"=\$1,"updated_at"=\$2 WHERE`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, "admin@example.com"))

	email, level, active := " admin@example.com ", model.LevelAdmin, true
	got, err := svc.UpdateUser(1, operator, UpdateUserRequest{Email: &email, Level: &level, IsActive: &active})
	if err != nil {
		t.Fatalf("管理员更新自己的邮箱应成功，得到 %v", err)
	}
	if got.Email != "admin@example.com" {
		t.Fatalf("返回的邮箱 = %q", got.Email)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestSelfLevelChangeStillRejected 锁住防提权规则：放宽「值未变」之后，
// 真正改自己等级或启用状态的请求仍必须被拒，且不发任何 UPDATE。
func TestSelfLevelChangeStillRejected(t *testing.T) {
	cases := map[string]UpdateUserRequest{
		"降自己等级": {Level: intPtr(model.LevelEditor)},
		"停用自己":  {IsActive: boolPtr(false)},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			db, mock, cleanup := testutil.NewMockDB(t)
			defer cleanup()
			mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, ""))

			operator := testutil.CreateTestUser(t, "admin", "admin", "")
			if _, err := NewAuthService(db).UpdateUser(1, operator, req); !errors.Is(err, ErrSelfOperation) {
				t.Fatalf("期望 ErrSelfOperation，得到 %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestUpdateUserRejectsMalformedEmail 防的是脏邮箱入库：Gravatar 哈希与邮箱
// 登录都依赖它，「Name <a@b>」这类值会让头像和登录同时静默失效。
func TestUpdateUserRejectsMalformedEmail(t *testing.T) {
	for _, bad := range []string{"no-at-sign", "a@", "@b.com", "a b@c.com", "Name <a@b.com>"} {
		db, mock, cleanup := testutil.NewMockDB(t)
		mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, ""))
		operator := testutil.CreateTestUser(t, "admin", "admin", "")
		email := bad
		if _, err := NewAuthService(db).UpdateUser(1, operator, UpdateUserRequest{Email: &email}); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("%q: 期望 ErrInvalidEmail，得到 %v", bad, err)
		}
		cleanup()
	}
}

// TestUpdateUserReportsDuplicateEmailAsTypedError 让 handler 能返回 409 与可读原因，
// 而不是笼统的 500「更新资料失败」。
func TestUpdateUserReportsDuplicateEmailAsTypedError(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, ""))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	operator := testutil.CreateTestUser(t, "admin", "admin", "")
	email := "taken@example.com"
	if _, err := NewAuthService(db).UpdateUser(1, operator, UpdateUserRequest{Email: &email}); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("期望 ErrEmailExists，得到 %v", err)
	}
}

// TestOperatorCannotSelfEscalateViaOutOfRangeLevel 锁住「值未变即放行」的边界：
// 2 级提交 level=0 会被 NormalizeLevel 夹成 1，与自身等级不同，必须仍按
// 改自己等级拒绝，绝不能因为归一化而落到「未变化」分支。
func TestOperatorCannotSelfEscalateViaOutOfRangeLevel(t *testing.T) {
	for _, req := range []UpdateUserRequest{{Level: intPtr(0)}, {Role: strPtr("admin")}} {
		db, mock, cleanup := testutil.NewMockDB(t)
		op := testutil.CreateTestUser(t, "op", "admin", "")
		op.Level = model.LevelOperator
		rows := sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(op)...)
		mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(rows)

		if _, err := NewAuthService(db).UpdateUser(op.ID, op, req); !errors.Is(err, ErrSelfOperation) {
			t.Errorf("%+v: 期望 ErrSelfOperation，得到 %v", req, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		cleanup()
	}
}

// TestUnchangedLegacyEmailDoesNotBlockOtherEdits 防的是收紧邮箱校验后的连带伤害：
// 库里已有的不规范邮箱会被表单原样回传，若无条件校验，这个用户连用户名都改不了。
func TestUnchangedLegacyEmailDoesNotBlockOtherEdits(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, "legacy-no-at"))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE "users" SET "username"=\$1`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT \* FROM "users"`).WillReturnRows(adminSelfRows(t, "legacy-no-at"))

	operator := testutil.CreateTestUser(t, "admin", "admin", "legacy-no-at")
	name, email := "root", "legacy-no-at"
	if _, err := NewAuthService(db).UpdateUser(1, operator, UpdateUserRequest{Username: &name, Email: &email}); err != nil {
		t.Fatalf("未改动的历史邮箱不应阻止改用户名，得到 %v", err)
	}
}

// TestCreateUserRejectsMalformedEmail 让入口与编辑走同一条校验，脏邮箱从源头进不来。
func TestCreateUserRejectsMalformedEmail(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	admin := testutil.CreateTestUser(t, "admin", "admin", "")
	if _, err := NewAuthService(db).CreateUser(admin, "u1", "password123", model.LevelViewer, "Name <a@b.com>"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("期望 ErrInvalidEmail，得到 %v", err)
	}
}
