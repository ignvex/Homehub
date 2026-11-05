package service

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/ignvex/HomeHub/internal/model"
)

// 本文件在真实 PostgreSQL 上验证单条 CASE 排序语句，只在设置 HOMEHUB_TEST_DB 时运行。
//
// sqlmock 只能证明「发出的 SQL 长这样」，证明不了 PostgreSQL 接受它：
// CASE 分支里的参数类型推断、IN 列表展开，都是 mock 看不到的。

// sortIntegrationSchema 是本文件独占的 schema，用完 DROP。
const sortIntegrationSchema = "it_service_sort"

func sortIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	if err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + sortIntegrationSchema).Error; err != nil {
		t.Fatalf("创建测试 schema 失败: %v", err)
	}
	if sqlDB, err := admin.DB(); err == nil {
		sqlDB.Close()
	}

	db, err := gorm.Open(postgres.Open(dsn+" search_path="+sortIntegrationSchema+",public"),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.Close()
		if cleanupDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard}); err == nil {
			cleanupDB.Exec("DROP SCHEMA IF EXISTS " + sortIntegrationSchema + " CASCADE")
			if s, err := cleanupDB.DB(); err == nil {
				s.Close()
			}
		}
	})

	if err := db.AutoMigrate(&model.BookmarkGroup{}, &model.Bookmark{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	return db
}

// TestIntegrationReorderBookmarksStaysInGroup 走真实库：排序生效，且别的分组的
// 书签即使混进 ID 列表也不会被改动。
func TestIntegrationReorderBookmarksStaysInGroup(t *testing.T) {
	db := sortIntegrationDB(t)
	svc := NewBookmarkService(db)

	g1, err := svc.CreateGroup("g1")
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	g2, err := svc.CreateGroup("g2")
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	a, _ := svc.CreateBookmark(g1.ID, "a", "https://a.example", "")
	b, _ := svc.CreateBookmark(g1.ID, "b", "https://b.example", "")
	other, _ := svc.CreateBookmark(g2.ID, "x", "https://x.example", "")
	if a == nil || b == nil || other == nil {
		t.Fatal("创建书签失败")
	}

	if err := svc.ReorderBookmarks(g1.ID, []uint64{b.ID, a.ID, other.ID}); err != nil {
		t.Fatalf("排序失败: %v", err)
	}

	order := func(id uint64) int {
		var bm model.Bookmark
		if err := db.First(&bm, id).Error; err != nil {
			t.Fatalf("读取书签失败: %v", err)
		}
		return bm.SortOrder
	}
	if order(b.ID) != 1 || order(a.ID) != 2 {
		t.Fatalf("排序未生效: b=%d a=%d", order(b.ID), order(a.ID))
	}
	if order(other.ID) != 1 {
		t.Fatalf("其他分组的书签被改动: sort_order=%d", order(other.ID))
	}

	if err := svc.ReorderGroups([]uint64{g2.ID, g1.ID}); err != nil {
		t.Fatalf("分组排序失败: %v", err)
	}
	var got model.BookmarkGroup
	db.First(&got, g2.ID)
	if got.SortOrder != 1 {
		t.Fatalf("分组排序未生效: g2=%d", got.SortOrder)
	}
}
