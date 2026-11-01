package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ignvex/HomeHub/internal/model"
)

// 本文件守护一条底线：代码里出现的 DDL 只能碰 HomeHub 自己的表。
//
// 同一个数据库还被独立的 B 站爬虫项目共用。如果哪天有人在此仓库
// 写下 DROP TABLE 或针对 bilibili_* / crawler_state 的 DDL，
// 那个项目的数据会被毁掉。这些约束靠测试兜住，而不是靠记性。
//
// 表名曾经统一带 homehub_ 前缀，靠前缀就足以隔离；前缀去掉后改用
// **显式允许列表**：新增表必须同步登记，否则测试失败。这点摩擦是
// 故意的——它让“不小心写到库里的别人的表”变成一次显式决定。

var (
	// 表结构变更只允许 CREATE TABLE / CREATE INDEX（幂等且非破坏性）。
	forbiddenDDLRe = regexp.MustCompile(`(?i)\b(DROP\s+TABLE|DROP\s+INDEX|DROP\s+COLUMN|DROP\s+SCHEMA|TRUNCATE|ALTER\s+TABLE)\b`)

	// 提取 CREATE TABLE 的目标表名。
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][\w.]*)`)

	// 提取 CREATE INDEX ... ON <table> 的目标表名。
	createIndexRe = regexp.MustCompile(`(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?\S+\s+ON\s+([a-zA-Z_][\w.]*)`)

	// 任何引用到 B 站爬虫表的词。
	bilibiliTableRe = regexp.MustCompile(`(?i)\b(bilibili_\w+|bili_users|crawler_state|video_pages|video_stats_snapshots|article_stats_snapshots|user_stats_snapshots)\b`)
)

// homeHubTables 是本仓库拥有的全部表名。
//
// 新增表时**必须**在这里加一行，否则 TestDDLOnlyTargetsHomeHubTables 会失败。
// 这个约束比旧的前缀检查更强：任何不在列表里的表名都会被拦下。
var homeHubTables = map[string]struct{}{
	"users":              {},
	"sessions":           {},
	"avatar_caches":      {},
	"bookmark_groups":    {},
	"bookmarks":          {},
	"media_assets":       {},
	"log_entries":        {},
	"rss_groups":         {},
	"rss_feeds":          {},
	"rss_entries":        {},
	"rss_entry_feeds":    {},
	"rss_images":         {},
	"rss_image_sources":  {},
	"rss_image_refs":     {},
	"todos":              {},
	"pastes":             {},
	"balance_targets":    {},
	"extension_hub_keys": {},
}

// ownedTable 判断表名是否属于本仓库（忽略 schema 限定符）。
func ownedTable(name string) bool {
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	_, ok := homeHubTables[name]
	return ok
}

// stripLineComments 去掉 Go 的 `//` 行注释，避免文档说明里的关键字被误判。
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// goSources 收集 internal/ 与 main.go 下所有非测试 Go 源文件。
func goSources(t *testing.T) []string {
	t.Helper()

	var files []string
	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "dist" || name == "web" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 Go 源文件失败: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("没有找到任何 Go 源文件")
	}
	return files
}

// TestNoDestructiveDDL 确认代码里不存在删表、删列、改表结构这类破坏性操作。
func TestNoDestructiveDDL(t *testing.T) {
	for _, path := range goSources(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", path, err)
		}
		for lineNo, line := range strings.Split(stripLineComments(string(raw)), "\n") {
			if m := forbiddenDDLRe.FindString(line); m != "" {
				t.Errorf("%s:%d 含破坏性 DDL %q，删表/改列需人工执行，不能出现在启动流程里:\n\t%s",
					path, lineNo+1, m, strings.TrimSpace(line))
			}
		}
	}
}

// TestDDLOnlyTargetsHomeHubTables 确认所有 CREATE TABLE / CREATE INDEX 只作用于
// 本仓库拥有的表。
//
// 这条同时挡住两种事故：写到 B 站爬虫的表上，以及写出未登记的新表名。
func TestDDLOnlyTargetsHomeHubTables(t *testing.T) {
	if len(homeHubTables) == 0 {
		t.Fatal("homeHubTables 为空，检查将形同虚设")
	}

	for _, path := range goSources(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", path, err)
		}
		src := stripLineComments(string(raw))

		for _, re := range []*regexp.Regexp{createTableRe, createIndexRe} {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				table := m[1]
				if !ownedTable(table) {
					t.Errorf("%s 的 DDL 目标表 %q 不在 homeHubTables 里；\n"+
						"若是本仓库的新表，请登记到 ddl_guard_test.go；若目标是别人的表，请删除该语句",
						path, table)
				}
			}
		}

		if m := bilibiliTableRe.FindString(src); m != "" {
			t.Errorf("%s 引用了 B 站爬虫的表 %q；本仓库不得对其做任何 DDL", path, m)
		}
	}
}

// TestAllModelsHaveRegisteredTable 确认每个 model 的 TableName 都登记在
// homeHubTables 中。
//
// AutoMigrate 不走 Go 源码里的 CREATE TABLE 字符串，上面那个测试看不到它，
// 所以需要单独把模型表名与列表对齐。

// TestEnsureSearchIndexesIsSafe 确认搜索索引的 DDL 全部是幂等的 CREATE INDEX IF NOT EXISTS。
//
// 这些语句在每次启动时执行，必须可重复运行且不破坏数据。
func TestEnsureSearchIndexesIsSafe(t *testing.T) {
	if len(searchIndexes) == 0 {
		t.Fatal("searchIndexes 为空，模糊搜索将退化为全表扫描")
	}

	for _, idx := range searchIndexes {
		if idx.name == "" {
			t.Error("存在未命名的索引定义")
		}
		upper := strings.ToUpper(idx.sql)
		if !strings.HasPrefix(strings.TrimSpace(upper), "CREATE INDEX IF NOT EXISTS") {
			t.Errorf("索引 %s 的 DDL 必须是幂等的 CREATE INDEX IF NOT EXISTS:\n\t%s", idx.name, idx.sql)
		}
		if !strings.Contains(idx.sql, "gin_trgm_ops") {
			t.Errorf("索引 %s 必须是 GIN + gin_trgm_ops 的模糊匹配索引:\n\t%s", idx.name, idx.sql)
		}
		// 用允许列表校验目标表，而不是前缀。
		if !targetsOwnedTable(idx.sql) {
			t.Errorf("索引 %s 未指向本仓库的表:\n\t%s", idx.name, idx.sql)
		}
	}
}

// targetsOwnedTable 判断一条 CREATE INDEX 语句是否落在本仓库的表上。
func targetsOwnedTable(sql string) bool {
	m := createIndexRe.FindStringSubmatch(sql)
	if len(m) < 2 {
		return false
	}
	return ownedTable(m[1])
}
func TestAllModelsHaveRegisteredTable(t *testing.T) {
	models := []struct {
		name  string
		table string
	}{
		{"User", model.User{}.TableName()},
		{"Session", model.Session{}.TableName()},
		{"AvatarCache", model.AvatarCache{}.TableName()},
		{"BookmarkGroup", model.BookmarkGroup{}.TableName()},
		{"Bookmark", model.Bookmark{}.TableName()},
		{"MediaAsset", model.MediaAsset{}.TableName()},
		{"LogEntry", model.LogEntry{}.TableName()},
		{"RSSGroup", model.RSSGroup{}.TableName()},
		{"RSSFeed", model.RSSFeed{}.TableName()},
		{"RSSEntry", model.RSSEntry{}.TableName()},
		{"RSSEntryFeed", model.RSSEntryFeed{}.TableName()},
		{"RSSImage", model.RSSImage{}.TableName()},
		{"RSSImageSource", model.RSSImageSource{}.TableName()},
		{"RSSImageRef", model.RSSImageRef{}.TableName()},
		{"Todo", model.Todo{}.TableName()},
		{"Paste", model.Paste{}.TableName()},
		{"BalanceTarget", model.BalanceTarget{}.TableName()},
		{"ExtensionHubKey", model.ExtensionHubKey{}.TableName()},
	}

	for _, m := range models {
		if m.table == "" {
			t.Errorf("%s.TableName() 返回空串", m.name)
			continue
		}
		if !ownedTable(m.table) {
			t.Errorf("%s 的表名 %q 未登记到 homeHubTables", m.name, m.table)
		}
	}

	// 反向检查：列表里有、但没有对应模型的表名，多半是改表名时漏改了。
	used := make(map[string]struct{}, len(models))
	for _, m := range models {
		used[m.table] = struct{}{}
	}
	for table := range homeHubTables {
		if _, ok := used[table]; !ok {
			t.Errorf("homeHubTables 里的 %q 没有对应的模型（改名残留？）", table)
		}
	}
}
