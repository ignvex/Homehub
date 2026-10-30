package service

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
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

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "users" WHERE username = $1 AND "users"."deleted_at" IS NULL ORDER BY "users"."id" LIMIT $2`)).
		WithArgs("admin", 1).
		WillReturnRows(sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(user)...))
	// session 创建
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "sessions"`)).
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

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "users" WHERE username = $1 AND "users"."deleted_at" IS NULL ORDER BY "users"."id" LIMIT $2`)).
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

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "users" WHERE username = $1 AND "users"."deleted_at" IS NULL ORDER BY "users"."id" LIMIT $2`)).
		WithArgs("nonexistent", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE \(email = \$1 AND email != ''\) AND "users"."deleted_at" IS NULL ORDER BY "users"."id" LIMIT \$2`).
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
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "users" WHERE username = $1 AND "users"."deleted_at" IS NULL`)).
		WithArgs("newuser").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// INSERT（无 BEGIN/COMMIT，因 SkipDefaultTransaction）
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "users"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))

	admin := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}
	user, err := svc.CreateUser(admin, "newuser", "password123", model.LevelViewer, "")
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

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "users" WHERE username = $1 AND "users"."deleted_at" IS NULL`)).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	admin := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}
	_, err := svc.CreateUser(admin, "admin", "password123", model.LevelViewer, "")
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("期望 ErrUsernameExists，得到: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateUser_InvalidLevel(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	admin := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}
	_, err := svc.CreateUser(admin, "newuser", "password123", 99, "")
	if err == nil {
		t.Fatal("越界等级应被拒绝")
	}
}

// TestCreateUser_CannotEscalate 锁住提权后门：
// 2 级运维不能创建 1 级管理员，否则每个中间等级都是一条直达最高权限的路。
func TestCreateUser_CannotEscalate(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	operator := &model.User{BaseModel: model.BaseModel{ID: 2}, Level: model.LevelOperator}

	// 造 1 级：拒绝
	if _, err := svc.CreateUser(operator, "root2", "password123", model.LevelAdmin, ""); err == nil {
		t.Error("2 级不应能创建 1 级账号")
	}
	// 造同级：也拒绝（否则可复制同类账号）
	if _, err := svc.CreateUser(operator, "peer", "password123", model.LevelOperator, ""); err == nil {
		t.Error("2 级不应能创建同级账号")
	}
}

// TestCreateUser_NilOperatorRejected 确认未认证时不能创建用户。
func TestCreateUser_NilOperatorRejected(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	if _, err := svc.CreateUser(nil, "x", "password123", model.LevelViewer, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，得到: %v", err)
	}
}

func TestCreateUser_ShortPassword(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	admin := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}
	_, err := svc.CreateUser(admin, "newuser", "12345", model.LevelViewer, "")
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Fatalf("期望 ErrPasswordTooWeak，得到: %v", err)
	}
}

func TestDeleteUser_SelfProtection(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewAuthService(db)

	operator := &model.User{BaseModel: model.BaseModel{ID: 1}, Level: model.LevelAdmin}
	err := svc.DeleteUser(1, operator) // targetID == operatorID
	if !errors.Is(err, ErrSelfOperation) {
		t.Fatalf("期望 ErrSelfOperation，得到: %v", err)
	}
}
