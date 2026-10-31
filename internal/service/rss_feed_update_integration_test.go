package service

import (
	"os"
	"testing"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 本文件是订阅源分组更新的真实数据库验证，只在设置 HOMEHUB_TEST_DB 时运行。
//
// sqlmock 已经能证明"生成了 SET group_id = $1 且参数为 nil"，但它证明不了
// PostgreSQL 收到 nil 之后那一列真的变成了 NULL —— 也可能被列默认值、
// 触发器或 NOT NULL 约束改写。分组清空是用户可见的行为，值得落到真实库上验一次。
//
// 运行方式：
//
//	HOMEHUB_TEST_DB="host=192.168.1.2 user=test password=xxx dbname=testdb sslmode=disable" \
//	  go test ./internal/service/ -run IntegrationFeedGroup -v

// feedIntegrationSchema 是订阅源更新测试专用 schema。
//
// 与图片集成测试分开：两者都会 DROP SCHEMA，共用一个名字时后置的 cleanup
// 会连带删掉另一个测试正在用的表。独立 schema 让每个测试只清理自己那份。
const feedIntegrationSchema = "it_service_feed"

func feedIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	if err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + feedIntegrationSchema).Error; err != nil {
		t.Fatalf("创建测试 schema 失败: %v", err)
	}
	if sqlDB, err := admin.DB(); err == nil {
		sqlDB.Close()
	}

	db, err := gorm.Open(postgres.Open(dsn+" search_path="+feedIntegrationSchema+",public"),
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
			cleanupDB.Exec("DROP SCHEMA IF EXISTS " + feedIntegrationSchema + " CASCADE")
			if s, err := cleanupDB.DB(); err == nil {
				s.Close()
			}
		}
	})

	if err := db.AutoMigrate(&model.RSSGroup{}, &model.RSSFeed{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	return db
}

// TestIntegrationFeedGroupClearAndRestore 走完整链路：分组 → 未分组 → 再分组。
//
// 单向断言不够：如果清除只是因为 UPDATE 根本没执行，那"变成 NULL"也会成立
// （前提是没设置过）。所以先把分组设上、读回来确认写进去了，再清除、再读回。
func TestIntegrationFeedGroupClearAndRestore(t *testing.T) {
	db := feedIntegrationDB(t)

	group := model.RSSGroup{Name: "测试分组"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	feed := model.RSSFeed{Title: "源", URL: "https://example.com/feed.xml", GroupID: &group.ID}
	if err := db.Create(&feed).Error; err != nil {
		t.Fatalf("创建订阅源失败: %v", err)
	}

	svc := &RSSService{db: db}

	// 先确认初始分组确实落库，避免"清除成功"只是因为一开始就是空。
	readGroup := func() *uint64 {
		t.Helper()
		var got model.RSSFeed
		if err := db.First(&got, feed.ID).Error; err != nil {
			t.Fatalf("读取订阅源失败: %v", err)
		}
		return got.GroupID
	}
	if initial := readGroup(); initial == nil || *initial != group.ID {
		t.Fatalf("初始 group_id = %v，期望 %d", initial, group.ID)
	}

	if err := svc.UpdateFeed(feed.ID, "", 0, GroupUpdate{Set: true, Value: nil}); err != nil {
		t.Fatalf("清空分组失败: %v", err)
	}
	if got := readGroup(); got != nil {
		t.Fatalf("清空后 group_id = %d，期望 NULL", *got)
	}

	if err := svc.UpdateFeed(feed.ID, "", 0, GroupUpdate{Set: true, Value: &group.ID}); err != nil {
		t.Fatalf("重新设置分组失败: %v", err)
	}
	if got := readGroup(); got == nil || *got != group.ID {
		t.Fatalf("重新设置后 group_id = %v，期望 %d", got, group.ID)
	}
}

// TestIntegrationFeedGroupUntouchedWhenNotSet 确认"没带这个字段"不会误清分组。
//
// 这条防的是反向回归：为了修清除功能，把 Set 判断写松，结果每次改标题
// 都会把订阅源踢出分组。
func TestIntegrationFeedGroupUntouchedWhenNotSet(t *testing.T) {
	db := feedIntegrationDB(t)

	group := model.RSSGroup{Name: "保留分组"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	feed := model.RSSFeed{Title: "旧标题", URL: "https://example.com/b.xml", FetchInterval: 3600, GroupID: &group.ID}
	if err := db.Create(&feed).Error; err != nil {
		t.Fatalf("创建订阅源失败: %v", err)
	}

	svc := &RSSService{db: db}
	if err := svc.UpdateFeed(feed.ID, "新标题", 7200, GroupUpdate{}); err != nil {
		t.Fatalf("只改标题失败: %v", err)
	}

	var got model.RSSFeed
	if err := db.First(&got, feed.ID).Error; err != nil {
		t.Fatalf("读取订阅源失败: %v", err)
	}
	if got.Title != "新标题" || got.FetchInterval != 7200 {
		t.Errorf("标题/间隔 = %q/%d，期望 新标题/7200", got.Title, got.FetchInterval)
	}
	if got.GroupID == nil || *got.GroupID != group.ID {
		t.Errorf("group_id = %v，期望仍为 %d（未带该字段时不该改动）", got.GroupID, group.ID)
	}
}
