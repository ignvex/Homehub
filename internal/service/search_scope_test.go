package service

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 待办搜索匹配标题与描述，排除软删除；片段在 SQL 里按字符截取，
// 不把整段描述拉回应用层。
func TestSearchTodosMatchesTitleAndDescription(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM todos t WHERE t.deleted_at IS NULL AND \(t.title ILIKE \$1 OR t.description ILIKE \$2\)`).
		WithArgs("%牛奶%", "%牛奶%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT t.id, t.title, t.status, t.due_date, t.created_at,.*substr\(t.description, greatest\(strpos\(lower\(t.description\), lower\(\$1::text\)\) - 120, 1\), 400\).*FROM todos t`).
		WithArgs("牛奶", "%牛奶%", "%牛奶%", "%牛奶%", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "status", "due_date", "created_at", "desc_window"}).
			AddRow(3, "买东西", "pending", nil, time.Now(), "记得买牛奶和面包"))

	res, err := NewSearchService(db).Search(SearchOptions{Query: "牛奶", Scope: "todos"})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
	if res.TodosTotal != 1 || len(res.Todos) != 1 || res.Todos[0].Snippet != "记得买牛奶和面包" {
		t.Fatalf("待办结果不符: %+v", res)
	}
	if res.Bookmarks == nil || res.RssEntries == nil || res.Pastes == nil {
		t.Fatal("未搜索的分组也应是空数组而不是 null")
	}
}

// 粘贴搜索只看未过期的，且 SELECT 里绝不出现 delete_token：
// 搜索结果对所有登录用户可见，带出删除密钥就等于谁都能删。
func TestSearchPastesExcludesExpiredAndNeverSelectsDeleteToken(t *testing.T) {
	loadConfigWith(t, `, "pastebin": {"enabled": true}`)
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM pastes p WHERE p.deleted_at IS NULL AND p.expires_at > \$1 AND p.content ILIKE \$2`).
		WithArgs(sqlmock.AnyArg(), "%key%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT p.id, p.public_id, p.created_at, p.expires_at,.*AS paste_window\s+FROM pastes p`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "public_id", "created_at", "expires_at", "paste_window"}).
			AddRow(1, "AbCdEfGh", time.Now(), time.Now().Add(time.Hour), "api key = xyz"))

	res, err := NewSearchService(db).Search(SearchOptions{Query: "key", Scope: "pastes"})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
	if res.PastesTotal != 1 || res.Pastes[0].PublicID != "AbCdEfGh" || res.Pastes[0].Snippet != "api key = xyz" {
		t.Fatalf("粘贴结果不符: %+v", res.Pastes)
	}
}

// 源码级断言：粘贴搜索的 SQL 不得引用 delete_token。
// 上面的 sqlmock 用例只匹配了前缀，这条兜住「有人往 SELECT 里加列」。
func TestSearchPastesSQLHasNoDeleteToken(t *testing.T) {
	src := searchPastesSourceForTest(t)
	if strings.Contains(src, "delete_token") {
		t.Fatal("searchPastes 引用了 delete_token")
	}
}

// 剪贴板关闭时不发任何 SQL，结果为空：搜索不能绕过开关。
func TestSearchPastesDisabledReturnsEmpty(t *testing.T) {
	loadConfigWith(t, `, "pastebin": {"enabled": false}`)
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	res, err := NewSearchService(db).Search(SearchOptions{Query: "x", Scope: "pastes"})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if len(res.Pastes) != 0 || res.PastesTotal != 0 {
		t.Fatalf("关闭时应为空: %+v", res.Pastes)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("不应发 SQL: %v", err)
	}
}

// scope=all 覆盖四类来源。用无序 mock：四个查询并发执行。
func TestSearchAllIncludesTodosAndPastes(t *testing.T) {
	loadConfigWith(t, `, "pastebin": {"enabled": true}`)
	db, mock, cleanup := testutil.NewUnorderedMockDB(t)
	defer cleanup()

	for _, table := range []string{"bookmarks b", "rss_entries re", "todos t", "pastes p"} {
		mock.ExpectQuery(`SELECT count\(\*\)\s+FROM ` + table).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	}
	if _, err := NewSearchService(db).Search(SearchOptions{Query: "x"}); err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("scope=all 未覆盖全部来源: %v", err)
	}
}

// 片段截取不得切碎多字节字符。
func TestPlainSnippetKeepsUTF8AndCode(t *testing.T) {
	text := strings.Repeat("中", 200) + "关键" + strings.Repeat("文", 200)
	got := plainSnippet(text, "关键")
	if !strings.Contains(got, "关键") || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("片段损坏: %q", got)
	}
	// 纯文本里的 <T> 不是标签。
	if got := plainSnippet("func Map<T>(x T)", "Map"); got != "func Map<T>(x T)" {
		t.Fatalf("plainSnippet 不应去掉尖括号: %q", got)
	}
}

func searchPastesSourceForTest(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("search_service.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "func (s *SearchService) searchPastes(")
	j := strings.Index(src[i:], "\n}\n")
	return src[i : i+j]
}

// 真实数据库：片段窗口 SQL（strpos/substr/::text）能被 PostgreSQL 接受，
// 过期粘贴不出现，软删除的待办不出现。
func TestIntegrationSearchTodosAndPastes(t *testing.T) {
	db := stateIntegrationDB(t)
	loadConfigWith(t, `, "pastebin": {"enabled": true}`)

	must(t, db.Create(&model.Todo{Title: "买牛奶", Status: "pending", Description: "去超市买牛奶 100%"}).Error)
	gone := model.Todo{Title: "牛奶（已删）", Status: "pending"}
	must(t, db.Create(&gone).Error)
	must(t, db.Delete(&gone).Error)
	must(t, db.Create(&model.Paste{PublicID: "live0001", Content: "secret 牛奶 recipe", DeleteToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}).Error)
	must(t, db.Create(&model.Paste{PublicID: "dead0001", Content: "old 牛奶", DeleteToken: "tok", ExpiresAt: time.Now().Add(-time.Hour)}).Error)

	res, err := NewSearchService(db).Search(SearchOptions{Query: "牛奶"})
	must(t, err)
	if res.TodosTotal != 1 || res.Todos[0].Title != "买牛奶" || !strings.Contains(res.Todos[0].Snippet, "牛奶") {
		t.Fatalf("待办结果不符: %+v", res.Todos)
	}
	if res.PastesTotal != 1 || res.Pastes[0].PublicID != "live0001" || res.Pastes[0].Snippet != "secret 牛奶 recipe" {
		t.Fatalf("粘贴结果不符: %+v", res.Pastes)
	}
}
