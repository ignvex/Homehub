package service

import (
	"os"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 本文件是 RSS 阅读状态、OPML / 书签导入与待办归属的真实数据库验证，
// 只在设置 HOMEHUB_TEST_DB 时运行。
//
// sqlmock 只能证明 SQL 的形状；GROUPING SETS 的分组语义、ON CONFLICT
// DO UPDATE ... WHERE 的 RowsAffected 计数，只有真实 PostgreSQL 能证实。

// stateIntegrationSchema 是这些用例专用的 schema，与其他集成测试分开，
// 各自 DROP 时不会删到别人正在用的表。
const stateIntegrationSchema = "it_service_state"

// stateIntegrationDB 在独立 schema 里建全量表，返回连接。
func stateIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	// 先删后建：上次异常退出留下的 schema 不能污染本次断言。
	admin.Exec("DROP SCHEMA IF EXISTS " + stateIntegrationSchema + " CASCADE")
	if err := admin.Exec("CREATE SCHEMA " + stateIntegrationSchema).Error; err != nil {
		t.Fatalf("创建测试 schema 失败: %v", err)
	}
	if s, err := admin.DB(); err == nil {
		s.Close()
	}

	db, err := gorm.Open(postgres.Open(dsn+" search_path="+stateIntegrationSchema+",public"),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
		if c, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard}); err == nil {
			c.Exec("DROP SCHEMA IF EXISTS " + stateIntegrationSchema + " CASCADE")
			if s, err := c.DB(); err == nil {
				s.Close()
			}
		}
	})

	if err := db.AutoMigrate(model.AutoMigrateModels()...); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	if err := model.CreatePastesTable(db); err != nil {
		t.Fatalf("创建 pastes 表失败: %v", err)
	}
	return db
}

// TestIntegrationEntryStateAndUnreadCounts 走完整链路：
// 两个源共享一个条目 → 计数 → 单条已读 → 批量已读 → 计数与列表筛选。
func TestIntegrationEntryStateAndUnreadCounts(t *testing.T) {
	db := stateIntegrationDB(t)
	now := time.Now()

	group := model.RSSGroup{Name: "G"}
	must(t, db.Create(&group).Error)
	fa := model.RSSFeed{Title: "A", URL: "https://a.example/feed", GroupID: &group.ID}
	fb := model.RSSFeed{Title: "B", URL: "https://b.example/feed"}
	must(t, db.Create(&fa).Error)
	must(t, db.Create(&fb).Error)

	var entries []model.RSSEntry
	for i, link := range []string{"https://x/1", "https://x/2", "https://x/3"} {
		e := model.RSSEntry{Title: link, Link: link, Published: now.Add(time.Duration(-i) * time.Hour), FetchedAt: now}
		must(t, db.Create(&e).Error)
		entries = append(entries, e)
	}
	link := func(entry model.RSSEntry, feed model.RSSFeed) {
		must(t, db.Create(&model.RSSEntryFeed{EntryID: entry.ID, FeedID: feed.ID, Published: entry.Published, FirstSeenAt: now, LastSeenAt: now}).Error)
	}
	// 1、2 在 A；2、3 在 B：2 号是共享条目。
	link(entries[0], fa)
	link(entries[1], fa)
	link(entries[1], fb)
	link(entries[2], fb)

	svc := &RSSService{db: db}
	const alice, bob = uint64(1), uint64(2)

	c, err := svc.UnreadCounts(alice)
	must(t, err)
	// 总数去重为 3；A=2，B=2；分组 G 只含 A → 2；B 未分组不进 by_group。
	if c.Total != 3 || c.ByFeed[fa.ID] != 2 || c.ByFeed[fb.ID] != 2 || c.ByGroup[group.ID] != 2 || len(c.ByGroup) != 1 {
		t.Fatalf("初始计数不符: %+v", c)
	}

	must(t, svc.SetEntryRead(alice, entries[1].ID, true))
	// 再标一次是幂等的。
	must(t, svc.SetEntryRead(alice, entries[1].ID, true))
	must(t, svc.SetEntryStarred(alice, entries[2].ID, true))

	c, err = svc.UnreadCounts(alice)
	must(t, err)
	if c.Total != 2 || c.ByFeed[fa.ID] != 1 || c.ByFeed[fb.ID] != 1 {
		t.Fatalf("标记 2 号已读后计数不符: %+v", c)
	}
	// bob 不受 alice 影响。
	if cb, _ := svc.UnreadCounts(bob); cb.Total != 3 {
		t.Fatalf("bob 的未读数被 alice 影响: %+v", cb)
	}

	// 列表筛选
	views, total, err := svc.ListEntries(EntryFilter{Page: 1, PerPage: 20, UserID: alice, State: "unread"})
	must(t, err)
	if total != 2 || len(views) != 2 {
		t.Fatalf("unread 列表 total=%d len=%d", total, len(views))
	}
	views, _, err = svc.ListEntries(EntryFilter{Page: 1, PerPage: 20, UserID: alice, State: "starred"})
	must(t, err)
	if len(views) != 1 || views[0].ID != entries[2].ID || !views[0].IsStarred || views[0].IsRead {
		t.Fatalf("starred 列表不符: %+v", views)
	}

	// 批量：A 源全部已读。2 号已读不重复计入，只新标记 1 号。
	fid := fa.ID
	n, err := svc.MarkEntriesRead(alice, MarkReadInput{FeedID: &fid})
	must(t, err)
	if n != 1 {
		t.Fatalf("A 源批量已读应新标记 1 条，got %d", n)
	}
	// 全部已读：只剩 3 号（已有星标行 → 走 DO UPDATE 分支）。
	n, err = svc.MarkEntriesRead(alice, MarkReadInput{})
	must(t, err)
	if n != 1 {
		t.Fatalf("全部已读应新标记 1 条，got %d", n)
	}
	c, _ = svc.UnreadCounts(alice)
	if c.Total != 0 {
		t.Fatalf("全部已读后仍有未读: %+v", c)
	}

	// 取消已读与星标后，全空行被删除。
	must(t, svc.SetEntryRead(alice, entries[2].ID, false))
	must(t, svc.SetEntryStarred(alice, entries[2].ID, false))
	var rows int64
	db.Model(&model.RSSEntryState{}).Where("user_id = ? AND entry_id = ?", alice, entries[2].ID).Count(&rows)
	if rows != 0 {
		t.Fatalf("两列都清空后应删除状态行，仍有 %d 行", rows)
	}

	// 不存在的条目 → 404，且不写入悬挂行。
	if err := svc.SetEntryRead(alice, 999999, true); err != ErrRSSEntryNotFound {
		t.Fatalf("不存在的条目应返回 ErrRSSEntryNotFound，got %v", err)
	}

	// 删除源 B：3 号成为孤儿被删，它的状态行一并清理；2 号仍被 A 收录。
	must(t, svc.SetEntryStarred(bob, entries[2].ID, true))
	svc.images = &fakeImageCache{}
	must(t, svc.DeleteFeed(fb.ID))
	db.Model(&model.RSSEntryState{}).Where("entry_id = ?", entries[2].ID).Count(&rows)
	if rows != 0 {
		t.Fatalf("孤儿条目的状态行未清理: %d", rows)
	}
	db.Model(&model.RSSEntryState{}).Where("entry_id = ?", entries[1].ID).Count(&rows)
	if rows != 1 {
		t.Fatalf("仍被收录的条目的状态行不应被删: %d", rows)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
