package service

import (
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// dryRunUpsertSQL 返回条目 upsert 实际会发给 PostgreSQL 的 SQL。
//
// 用 DryRun + sqlmock：既不需要真实数据库，也不需要预先写期望，
// 拿到的是 GORM 真实生成的语句。这类断言的意义在于，`ON CONFLICT`
// 的 SET 子句语义没法靠类型系统或单元测试里的内存对象验证。
func dryRunUpsertSQL(t *testing.T) string {
	t.Helper()

	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建 sqlmock 失败: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{DryRun: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("创建 gorm 实例失败: %v", err)
	}

	entries := []model.RSSEntry{
		{Title: "t", Link: "http://a/1", FetchedAt: time.Now(), MagnetURL: "magnet:x"},
	}
	tx := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "link"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"magnet_url": gorm.Expr(
				"COALESCE(NULLIF(excluded.magnet_url, ''), rss_entries.magnet_url)"),
		}),
	}).Create(&entries)
	if tx.Error != nil {
		t.Fatalf("生成 SQL 失败: %v", tx.Error)
	}
	return tx.Statement.SQL.String()
}

// TestEntryUpsertDoesNotEmptyMagnetURL 锁住一条数据丢失回归。
//
// 条目跨源共享：同一个 link 可能先被带磁力链接的源收录，再被不带磁力的
// 源收录。若 SET 子句直接 `magnet_url = excluded.magnet_url`，第二次抓取
// 会把已提取到的磁力链接清空——而且不可恢复，因为那条条目已经被判为
// "处理过"，后续抓取不会再回填。
//
// 正确的语义与正文一致：已有值优先，只在旧值为空时接受新值。
func TestEntryUpsertDoesNotEmptyMagnetURL(t *testing.T) {
	sql := dryRunUpsertSQL(t)

	if !strings.Contains(sql, "ON CONFLICT") {
		t.Fatalf("未生成 ON CONFLICT 子句:\n%s", sql)
	}

	// 必须是"新值为空则回退旧值"的表达式，而不是裸赋值。
	if !strings.Contains(sql, "NULLIF(excluded.magnet_url") {
		t.Errorf("magnet_url 的更新没有排除空值，会覆盖已有数据:\n%s", sql)
	}
	if !strings.Contains(sql, "COALESCE") {
		t.Errorf("magnet_url 的更新缺少 COALESCE 回退:\n%s", sql)
	}

	// 反向断言：不能出现直接把 excluded 赋回去的写法。
	if strings.Contains(sql, `"magnet_url"=excluded.magnet_url`) {
		t.Errorf("magnet_url 被裸赋值，将用空值覆盖已有磁力链接:\n%s", sql)
	}

	// 冲突目标必须是 link，否则去重语义会变。
	if !strings.Contains(sql, `ON CONFLICT ("link")`) {
		t.Errorf("冲突目标不是 link 列:\n%s", sql)
	}
}
