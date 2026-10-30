package database

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// schemaRequirement 描述一次必须手工执行的结构迁移。
//
// 为什么不在启动时自动做：这些语句包含 ALTER TABLE / DROP INDEX，
// ddl_guard_test.go 明确禁止它们出现在启动流程里 —— 破坏性 DDL 一旦
// 在错误的库上跑，无法撤销。
type schemaRequirement struct {
	// check 返回 true 表示"旧结构仍然存在，需要迁移"。
	check func(db *gorm.DB) (bool, error)
	// target 说明缺哪张表的哪一列，用作错误信息。
	target string
}

// schemaRequirements 是当前版本依赖的手工迁移。
//
// 注意：SQL 本体不写在这里（会被 DDL 守卫当成启动流程里的破坏性语句），
// 统一放在 docs/migrations/ 下。
var schemaRequirements = []schemaRequirement{
	{
		// 条目改成跨源共享后不再写 feed_id；旧列是 NOT NULL 且无默认值，
		// 不放开约束会让所有插入失败。
		check: func(db *gorm.DB) (bool, error) {
			return columnIsNotNull(db, "homehub_rss_entries", "feed_id")
		},
		target: "homehub_rss_entries.feed_id 仍为 NOT NULL（条目已改为跨源共享，不再写该列）",
	},
	{
		// 图片引用改成 (image_id, entry_id)；旧列同样会拦截插入。
		check: func(db *gorm.DB) (bool, error) {
			return columnIsNotNull(db, "homehub_rss_image_refs", "feed_id")
		},
		target: "homehub_rss_image_refs.feed_id / entry_hash 仍为 NOT NULL（引用已改为指向条目）",
	},
}

// columnIsNotNull 判断某列是否存在且为 NOT NULL。
//
// 表或列不存在时返回 false：全新安装由 AutoMigrate 建表，不会遇到旧结构。
func columnIsNotNull(db *gorm.DB, table, column string) (bool, error) {
	var count int64
	err := db.Raw(`
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = ?
		  AND column_name = ?
		  AND is_nullable = 'NO'
	`, table, column).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CheckSchema 在启动时检查手工迁移是否已执行。
//
// 没执行时返回一个包含指引的错误：让服务起不来，好过让它起来之后每次
// 抓取都因为 NOT NULL 约束静默失败。
func CheckSchema(db *gorm.DB) error {
	if db == nil {
		return nil
	}

	var pending []string
	for _, req := range schemaRequirements {
		needed, err := req.check(db)
		if err != nil {
			return fmt.Errorf("检查数据库结构失败: %w", err)
		}
		if needed {
			pending = append(pending, req.target)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	return fmt.Errorf(
		"RSS 表结构需要手工迁移后才能启动：\n\n  - %s\n\n"+
			"迁移脚本见 docs/migrations/001-rss-shared-entries.sql，说明见 docs/tables.md 第 10 节。请先备份数据库再执行。",
		strings.Join(pending, "\n  - "),
	)
}
