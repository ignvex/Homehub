// Package testutil 提供 HomeHub 测试基础设施，包括 sqlmock 初始化和常用工厂函数。
//
// 使用方式：
//
//	db, mock, cleanup := testutil.NewMockDB(t)
//	defer cleanup()
//	svc := service.NewAuthService(db)
//	mock.ExpectQuery(...).WillReturnRows(...)
package testutil

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewMockDB 创建基于 sqlmock 的 *gorm.DB 实例，返回清理函数。
//
// 使用 postgres 驱动以确保与生产环境 SQL 语法一致。
func NewMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("创建 sqlmock 失败: %v", err)
	}

	dialector := postgres.New(postgres.Config{Conn: sqlDB})
	db, err := gorm.Open(dialector, &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("创建 gorm 实例失败: %v", err)
	}

	cleanup := func() {
		sqlDB.Close()
	}

	return db, mock, cleanup
}

// NewUnorderedMockDB 与 NewMockDB 相同，但关闭 SQL 期望的顺序匹配。
//
// 只用于并发场景：多个 goroutine 的查询会交错执行，严格顺序匹配会让
// 期望队列互相阻塞（表现为测试永久挂起）。单线程用例请继续用 NewMockDB，
// 顺序匹配能更早发现意外的额外查询。
func NewUnorderedMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("创建 sqlmock 失败: %v", err)
	}
	mock.MatchExpectationsInOrder(false)

	dialector := postgres.New(postgres.Config{Conn: sqlDB})
	db, err := gorm.Open(dialector, &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("创建 gorm 实例失败: %v", err)
	}

	cleanup := func() {
		sqlDB.Close()
	}

	return db, mock, cleanup
}

// CreateTestUser 在 mock DB 上创建一个测试用户记录（不涉及真实 DB）。
func CreateTestUser(t *testing.T, username, role, email string) *model.User {
	t.Helper()
	return &model.User{
		BaseModel: model.BaseModel{
			ID:        1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Username:     username,
		PasswordHash: "$2a$10$dummyhash...", // bcrypt.MinCost hash of "password"
		Email:        email,
		Role:         role,
		IsActive:     true,
	}
}

// ColumnsForUser 返回 User 模型的 SQL 列名，用于 sqlmock Row 构造。
func ColumnsForUser() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at",
		"username", "password_hash", "email", "role", "is_active"}
}

// UserToRow 将 *model.User 转换为 sqlmock 行值。
func UserToRow(u *model.User) []driver.Value {
	return []driver.Value{
		u.ID, u.CreatedAt, u.UpdatedAt, nil,
		u.Username, u.PasswordHash, u.Email, u.Role, u.IsActive,
	}
}

// driverValue 适配 sqlmock 的 driver.Value 类型。
type driverValue interface{}
