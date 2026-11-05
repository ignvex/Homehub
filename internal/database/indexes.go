package database

import (
	"github.com/ignvex/HomeHub/internal/logger"
	"gorm.io/gorm"
)

// searchIndexes 模糊搜索所需的 pg_trgm GIN 索引。
//
// 为什么不用 GORM 的 index tag：tag 写法（`type:gin,expression:title gin_trgm_ops`）
// 确实能生成同样的 DDL，但 AutoMigrate 建索引失败会直接返回错误，
// 而建索引依赖 pg_trgm 扩展、扩展又需要数据库账号有 CREATE EXTENSION 权限。
// 这些都是"没有索引也能跑"的纯性能优化，不该让服务起不来，
// 所以改用显式 DDL 并对失败降级。
var searchIndexes = []struct {
	name string
	sql  string
}{
	{
		name: "idx_bookmarks_title_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_bookmarks_title_trgm ON bookmarks USING GIN (title gin_trgm_ops)`,
	},
	{
		name: "idx_bookmarks_url_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_bookmarks_url_trgm ON bookmarks USING GIN (url gin_trgm_ops)`,
	},
	{
		name: "idx_rss_entries_title_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_rss_entries_title_trgm ON rss_entries USING GIN (title gin_trgm_ops)`,
	},
	{
		// 全局搜索对 RSS 条目匹配 title / content / author 三列（见
		// service.searchRssEntries）。正文没有单独的摘要列——摘要是
		// 列表查询时从 content 现算的——所以索引只能建在 content 上。
		// GIN trgm 存的是三元组而不是整列值，没有 btree 的单键字节上限，
		// 正文再长也建得出来；代价是索引体积与写入放大，换来的是
		// ILIKE '%词%' 不再全表扫描正文。
		name: "idx_rss_entries_content_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_rss_entries_content_trgm ON rss_entries USING GIN (content gin_trgm_ops)`,
	},
	{
		name: "idx_rss_entries_author_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_rss_entries_author_trgm ON rss_entries USING GIN (author gin_trgm_ops)`,
	},
	{
		// 待办搜索匹配 title / description。
		name: "idx_todos_title_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_todos_title_trgm ON todos USING GIN (title gin_trgm_ops)`,
	},
	{
		name: "idx_todos_description_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_todos_description_trgm ON todos USING GIN (description gin_trgm_ops)`,
	},
	{
		// 日志关键词搜索走 `message ILIKE '%...%'`，没有这个索引就是
		// 全表顺序扫描。日志表只增不减，是全库最可能变大的表，
		// 这个索引的收益随时间线性增长。
		name: "idx_log_entries_message_trgm",
		sql:  `CREATE INDEX IF NOT EXISTS idx_log_entries_message_trgm ON log_entries USING GIN (message gin_trgm_ops)`,
	},
}

// EnsureSearchIndexes 创建 pg_trgm 扩展与模糊搜索索引。
//
// 调用时机：必须在 AutoMigrate 之后（表需已存在）。
//
// 整个流程是尽力而为：扩展不可用（账号无权限、托管 PG 未预装）或
// 单个索引创建失败时只记录警告，服务照常启动，搜索退化为全表扫描。
// 数据量小时差异不明显；数据量大时可据此日志手动处理权限。
func EnsureSearchIndexes(db *gorm.DB) {
	if db == nil {
		return
	}

	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error; err != nil {
		logger.Warn("pg_trgm 扩展不可用，跳过模糊搜索索引（搜索将走全表扫描）", "error", err)
		return
	}

	created := 0
	for _, idx := range searchIndexes {
		if err := db.Exec(idx.sql).Error; err != nil {
			logger.Warn("创建模糊搜索索引失败", "index", idx.name, "error", err)
			continue
		}
		created++
	}

	logger.Info("模糊搜索索引就绪", "count", created)
}
