package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 本文件守护一条底线：代码里出现的 DDL 只能碰 HomeHub 自己的表。
//
// 同一个数据库还被独立的 B 站爬虫项目使用。如果哪天有人在此仓库
// 写下 DROP TABLE 或针对 bilibili_* / crawler_state 的 DDL，
// 那个项目的数据会被毁掉。这些约束靠测试兜住，而不是靠记性。

var (
	// 表结构变更只允许 CREATE TABLE / CREATE INDEX（幂等且非破坏性）。
	forbiddenDDLRe = regexp.MustCompile(`(?i)\b(DROP\s+TABLE|DROP\s+INDEX|DROP\s+COLUMN|DROP\s+SCHEMA|TRUNCATE|ALTER\s+TABLE)\b`)

	// 提取 CREATE TABLE 的目标表名。
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][\w.]*)`)

	// 提取 CREATE INDEX ... ON <table> 的目标表名。
	createIndexRe = regexp.MustCompile(`(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?\S+\s+ON\s+([a-zA-Z_][\w.]*)`)

	// 任何引用到 B 站爬虫表的词（改名前后两套写法）。
	bilibiliTableRe = regexp.MustCompile(`(?i)\b(bilibili_\w+|bili_users|crawler_state|homehub_crawler_state|video_pages|video_stats_snapshots|article_stats_snapshots|user_stats_snapshots)\b`)
)

// allowedTablePrefix 是本仓库唯一允许建表/建索引的表名前缀。
const allowedTablePrefix = "homehub_"

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

// TestDDLOnlyTargetsHomeHubTables 确认所有 CREATE TABLE / CREATE INDEX 只作用于 homehub_ 表。
//
// 这条同时挡住两种事故：写到 B 站爬虫的表上，以及写出无前缀的新表名。
func TestDDLOnlyTargetsHomeHubTables(t *testing.T) {
	for _, path := range goSources(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", path, err)
		}
		src := stripLineComments(string(raw))

		for _, re := range []*regexp.Regexp{createTableRe, createIndexRe} {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				table := m[1]
				if !strings.HasPrefix(table, allowedTablePrefix) {
					t.Errorf("%s 的 DDL 目标表 %q 缺少 %q 前缀；\n"+
						"若目标是 B 站爬虫的表，请删除该语句——那些表由独立项目管理",
						path, table, allowedTablePrefix)
				}
			}
		}

		if m := bilibiliTableRe.FindString(src); m != "" {
			t.Errorf("%s 引用了 B 站爬虫的表 %q；本仓库不得对其做任何 DDL", path, m)
		}
	}
}

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
		if !strings.Contains(upper, "GIN") || !strings.Contains(idx.sql, "gin_trgm_ops") {
			t.Errorf("索引 %s 必须是 GIN + gin_trgm_ops 的模糊匹配索引:\n\t%s", idx.name, idx.sql)
		}
		if !strings.Contains(idx.sql, allowedTablePrefix) {
			t.Errorf("索引 %s 未指向 %s 表:\n\t%s", idx.name, allowedTablePrefix, idx.sql)
		}
	}
}
