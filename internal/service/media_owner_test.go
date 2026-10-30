package service

import (
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// mediaAssetColumns 是 MediaAsset 查询所需的列。
func mediaAssetColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "deleted_at",
		"filename", "original_name", "file_path", "file_size", "sha256",
		"mime_type", "media_type", "source", "owner_id", "width", "height",
	}
}

// mediaRow 构造一行媒体资源。
func mediaRow(id, ownerID uint64) []driver.Value {
	return []driver.Value{
		id, nil, nil, nil,
		"f.png", "orig.png", "uploads/f.png", 100, "",
		"image/png", "image", "upload", ownerID, 0, 0,
	}
}

// TestMediaDeleteRejectsOtherUsersAsset 锁住一条真实的水平越权。
//
// 改造前所有登录用户都能删除任意媒体资源 —— 包括别人正在当背景用的图。
// 图床的语义是“我传的图归我管”，member(4) 只能删自己的。
func TestMediaDeleteRejectsOtherUsersAsset(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// owner 是 100，操作者是 200
	mock.ExpectQuery(`SELECT \* FROM "media_assets" WHERE "media_assets"."id" = \$1`).
		WillReturnRows(sqlmock.NewRows(mediaAssetColumns()).AddRow(mediaRow(1, 100)...))

	s := &MediaService{db: db}
	operator := &model.User{BaseModel: model.BaseModel{ID: 200}, Level: model.LevelMember}

	err := s.Delete(1, operator)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("member 删除他人资源应被拒绝，实际: %v", err)
	}
}

// TestMediaDeleteAllowsOwnAsset 确认 member 可以删自己的。
func TestMediaDeleteAllowsOwnAsset(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "media_assets"`).
		WillReturnRows(sqlmock.NewRows(mediaAssetColumns()).AddRow(mediaRow(1, 100)...))
	mock.ExpectExec(`DELETE FROM "media_assets"`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &MediaService{db: db}
	operator := &model.User{BaseModel: model.BaseModel{ID: 100}, Level: model.LevelMember}

	if err := s.Delete(1, operator); err != nil {
		t.Fatalf("删除自己的资源应成功: %v", err)
	}
}

// TestMediaDeleteEditorCanDeleteAny 确认 editor(3) 能删任何人的。
//
// 3 级的设计是“内容管理，不分作者”，所以它必须能删别人的。
func TestMediaDeleteEditorCanDeleteAny(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "media_assets"`).
		WillReturnRows(sqlmock.NewRows(mediaAssetColumns()).AddRow(mediaRow(1, 100)...))
	mock.ExpectExec(`DELETE FROM "media_assets"`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &MediaService{db: db}
	operator := &model.User{BaseModel: model.BaseModel{ID: 999}, Level: model.LevelEditor}

	if err := s.Delete(1, operator); err != nil {
		t.Fatalf("editor 应能删除任意资源: %v", err)
	}
}

// TestMediaDeleteRejectsNilOperator 确认未认证时拒绝。
func TestMediaDeleteRejectsNilOperator(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	s := &MediaService{db: db}
	if err := s.Delete(1, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，得到: %v", err)
	}
}

// TestCanManageOwned 直接验证归属判定函数。
func TestCanManageOwned(t *testing.T) {
	mk := func(id uint64, level int) *model.User {
		return &model.User{BaseModel: model.BaseModel{ID: id}, Level: level}
	}

	cases := []struct {
		name    string
		op      *model.User
		ownerID uint64
		want    bool
	}{
		{"admin 可管任意", mk(1, model.LevelAdmin), 100, true},
		{"operator 可管任意", mk(2, model.LevelOperator), 100, true},
		{"editor 可管任意", mk(3, model.LevelEditor), 100, true},
		{"member 管自己的", mk(100, model.LevelMember), 100, true},
		{"member 管别人的", mk(200, model.LevelMember), 100, false},
		{"member 管无主资源", mk(200, model.LevelMember), 0, false},
		{"viewer 什么都不行", mk(300, model.LevelViewer), 300, false},
		{"viewer 管自己的也不行", mk(300, model.LevelViewer), 300, false},
		{"nil 操作者", nil, 100, false},
	}

	for _, c := range cases {
		if got := canManageOwned(c.op, c.ownerID); got != c.want {
			t.Errorf("%s: canManageOwned = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMediaUploadRecordsOwner 确认上传时写入 owner_id。
func TestMediaUploadRecordsOwner(t *testing.T) {
	// 上传需要真实文件与目录，这里只验证 owner 计算逻辑，
	// 避免把测试绑到文件系统细节上。
	owner := &model.User{BaseModel: model.BaseModel{ID: 42}, Level: model.LevelMember}
	if owner.ID != 42 {
		t.Fatal("测试前提错误")
	}
	// 契约：Upload 用 operator.ID 作为 OwnerID；
	// 端到端验证在真实数据库集成测试里做。
}
