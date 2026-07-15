package service

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/testutil"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Phase 1 Slice C2: 认证安全网测试

func TestLogin_Success(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	password := "testpassword"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	user := testutil.CreateTestUser(t, "admin", "admin", "")
	user.PasswordHash = string(hash)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "homehub_users" WHERE username = $1 AND "homehub_users"."deleted_at" IS NULL ORDER BY "homehub_users"."id" LIMIT $2`)).
		WithArgs("admin", 1).
		WillReturnRows(sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(user)...))
	// session 创建
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "homehub_sessions"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	token, err := svc.Login("admin", password)
	if err != nil {
		t.Fatalf("登录应成功: %v", err)
	}
	if token == "" {
		t.Fatal("token 不应为空")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	password := "realpassword"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	user := testutil.CreateTestUser(t, "admin", "admin", "")
	user.PasswordHash = string(hash)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "homehub_users" WHERE username = $1 AND "homehub_users"."deleted_at" IS NULL ORDER BY "homehub_users"."id" LIMIT $2`)).
		WithArgs("admin", 1).
		WillReturnRows(sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(user)...))

	_, err := svc.Login("admin", "wrongpassword")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("期望 ErrAuthFailed，得到: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "homehub_users" WHERE username = $1 AND "homehub_users"."deleted_at" IS NULL ORDER BY "homehub_users"."id" LIMIT $2`)).
		WithArgs("nonexistent", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	mock.ExpectQuery(`SELECT \* FROM "homehub_users" WHERE \(email = \$1 AND email != ''\) AND "homehub_users"."deleted_at" IS NULL ORDER BY "homehub_users"."id" LIMIT \$2`).
		WithArgs("nonexistent", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := svc.Login("nonexistent", "any")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("期望 ErrAuthFailed，得到: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateUser_Success(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	// 检查用户名不存在
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "homehub_users" WHERE username = $1 AND "homehub_users"."deleted_at" IS NULL`)).
		WithArgs("newuser").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// INSERT（无 BEGIN/COMMIT，因 SkipDefaultTransaction）
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "homehub_users"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))

	user, err := svc.CreateUser("newuser", "password123", "user", "")
	if err != nil {
		t.Fatalf("创建用户应成功: %v", err)
	}
	if user.Username != "newuser" {
		t.Errorf("用户名应为 newuser，得到 %s", user.Username)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateUser_UsernameExists(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "homehub_users" WHERE username = $1 AND "homehub_users"."deleted_at" IS NULL`)).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	_, err := svc.CreateUser("admin", "password123", "user", "")
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("期望 ErrUsernameExists，得到: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	_, err := svc.CreateUser("newuser", "password123", "invalid_role", "")
	if err == nil || err.Error() != "角色必须为 admin 或 user" {
		t.Fatalf("期望角色校验错误，得到: %v", err)
	}
}

func TestCreateUser_ShortPassword(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	_, err := svc.CreateUser("newuser", "12345", "user", "")
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Fatalf("期望 ErrPasswordTooWeak，得到: %v", err)
	}
}

func TestDeleteUser_SelfProtection(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	err := svc.DeleteUser(1, 1) // targetID == operatorID
	if !errors.Is(err, ErrSelfOperation) {
		t.Fatalf("期望 ErrSelfOperation，得到: %v", err)
	}
}
