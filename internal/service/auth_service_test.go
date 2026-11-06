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

// disabledUserRows 返回一个已停用、密码为 password 的用户查询结果。
func disabledUserRows(t *testing.T, password string) *sqlmock.Rows {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	user := testutil.CreateTestUser(t, "admin", "admin", "")
	user.PasswordHash = string(hash)
	user.IsActive = false
	return sqlmock.NewRows(testutil.ColumnsForUser()).AddRow(testutil.UserToRow(user)...)
}

// TestLoginDisabledUserWithWrongPasswordIsAuthFailure 防的是借登录接口探测停用账号。
//
// 旧实现先判断 IsActive 再校验密码：随便填个密码就能区分「账号已停用」与
// 「密码错误」，而且停用分支的失败不计入锁定次数，可以无限试。
// 现在必须先过密码校验，错密码一律是 ErrAuthFailed。
func TestLoginDisabledUserWithWrongPasswordIsAuthFailure(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`FROM "users" WHERE username`).WillReturnRows(disabledUserRows(t, "realpassword"))

	if _, err := NewAuthService(db).Login("admin", "wrongpassword"); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("停用账号 + 错误密码应为 ErrAuthFailed，got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// TestLoginDisabledUserWithCorrectPasswordIsRejected 确认密码正确时仍拒绝停用账号，
// 且不创建会话（未为 INSERT 设期望，执行了 sqlmock 会报错）。
func TestLoginDisabledUserWithCorrectPasswordIsRejected(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`FROM "users" WHERE username`).WillReturnRows(disabledUserRows(t, "realpassword"))

	if _, err := NewAuthService(db).Login("admin", "realpassword"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("停用账号 + 正确密码应为 ErrUserDisabled，got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// TestLoginUnknownUserStillComparesPassword 防的是靠响应耗时枚举用户名。
//
// 用户存在时要做一次 bcrypt 比较（约几十毫秒），不存在时若直接返回，
// 两者耗时差一个数量级。不存在时也要对一个固定哈希比较一次。
func TestLoginUnknownUserStillComparesPassword(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	calls := 0
	prev := comparePassword
	comparePassword = func(hash, pw []byte) error {
		calls++
		if cost, err := bcrypt.Cost(hash); err != nil || cost != bcryptCost {
			t.Errorf("假比较用的哈希 cost = %d (%v)，应与真实哈希一致（%d）", cost, err, bcryptCost)
		}
		return prev(hash, pw)
	}
	t.Cleanup(func() { comparePassword = prev })

	mock.ExpectQuery(`FROM "users" WHERE username`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`FROM "users" WHERE \(email`).WillReturnError(gorm.ErrRecordNotFound)

	if _, err := NewAuthService(db).Login("nobody", "x"); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("期望 ErrAuthFailed，got %v", err)
	}
	if calls != 1 {
		t.Fatalf("用户不存在时 bcrypt 比较次数 = %d，期望 1", calls)
	}
}
