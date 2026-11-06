package service

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
	"gorm.io/gorm/schema"
)

// Chrome 导出的真实形态：<p> 与 <DT> 不闭合、文件夹多层嵌套、
// 根 <DL> 下也有散落书签。解析结果的分组名应拍平成「父/子」。
func TestParseNetscapeBookmarksFlattensNestedFolders(t *testing.T) {
	src := `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
    <DT><A HREF="https://loose.example/" ADD_DATE="1">Loose</A>
    <DT><H3 ADD_DATE="1">工作</H3>
    <DL><p>
        <DT><A HREF="https://a.example/?x=1&amp;y=2">A &amp; B</A>
        <DT><H3>前端</H3>
        <DL><p>
            <DT><A HREF="https://b.example/">B</A>
        </DL><p>
        <DT><A HREF="javascript:alert(1)">Evil</A>
    </DL><p>
</DL><p>`
	got, err := parseNetscapeBookmarks(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []parsedBookmark{
		{"", "Loose", "https://loose.example/"},
		{"工作", "A & B", "https://a.example/?x=1&y=2"},
		{"工作/前端", "B", "https://b.example/"},
		{"工作", "Evil", "javascript:alert(1)"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d 条: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 导出必须转义：书签标题里的 <script> 原样写进文件，浏览器导入时
// 就是一段可执行的 HTML。
func TestExportBookmarksHTMLEscapes(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Unix(1700000000, 0)
	mock.ExpectQuery(`SELECT \* FROM "bookmark_groups"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "deleted_at", "name", "sort_order", "priority"}).
			AddRow(1, now, now, nil, `R&D <x>`, 1, 0))
	mock.ExpectQuery(`SELECT \* FROM "bookmarks"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "deleted_at", "group_id", "title", "url", "icon", "sort_order", "priority"}).
			AddRow(5, now, now, nil, 1, `<script>alert(1)</script>`, `https://e.example/?a=1&b="2"`, "", 1, 0))

	out, err := NewBookmarkService(db).ExportBookmarksHTML()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<!DOCTYPE NETSCAPE-Bookmark-file-1>",
		`<H3 ADD_DATE="1700000000">R&amp;D &lt;x&gt;</H3>`,
		`<A HREF="https://e.example/?a=1&amp;b=&#34;2&#34;" ADD_DATE="1700000000">&lt;script&gt;alert(1)&lt;/script&gt;</A>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("导出缺少 %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<script>") {
		t.Errorf("标题未转义:\n%s", s)
	}
}

func TestImportBookmarksRejectsOversizedFile(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	big := strings.NewReader(strings.Repeat(" ", MaxBookmarkImportBytes+1))
	if _, err := NewBookmarkService(db).ImportBookmarksHTML(big); !errors.Is(err, ErrBookmarkImportTooLarge) {
		t.Fatalf("期望 ErrBookmarkImportTooLarge，got %v", err)
	}
}

// 真实数据库 round-trip：导出 → 导入到空库得到同样的分组与书签；
// 再导入一次全部跳过（同组同 URL 去重）。
func TestIntegrationBookmarkExportImportRoundTrip(t *testing.T) {
	db := stateIntegrationDB(t)
	svc := NewBookmarkService(db)

	g1, err := svc.CreateGroup("常用 & 工具")
	must(t, err)
	g2, err := svc.CreateGroup("中文分组")
	must(t, err)
	_, err = svc.CreateBookmark(g1.ID, "Go <dev>", "https://go.dev/?a=1&b=2", "")
	must(t, err)
	_, err = svc.CreateBookmark(g1.ID, "GitHub", "https://github.com/", "")
	must(t, err)
	_, err = svc.CreateBookmark(g2.ID, "中文标题", "https://example.com/中文", "")
	must(t, err)

	out, err := svc.ExportBookmarksHTML()
	must(t, err)

	// 导入到一个空库：先把数据清掉（只删本 schema 的行）。
	must(t, db.Exec("DELETE FROM bookmarks").Error)
	must(t, db.Exec("DELETE FROM bookmark_groups").Error)

	res, err := svc.ImportBookmarksHTML(strings.NewReader(string(out)))
	must(t, err)
	if res.Created != 3 || res.Skipped != 0 || res.GroupsCreated != 2 {
		t.Fatalf("round-trip 导入结果不符: %+v\n%s", res, out)
	}

	groups, err := svc.ListBookmarks()
	must(t, err)
	byName := map[string][]model.Bookmark{}
	for _, g := range groups {
		byName[g.Name] = g.Bookmarks
	}
	if bs := byName["常用 & 工具"]; len(bs) != 2 || bs[0].Title != "Go <dev>" || bs[0].URL != "https://go.dev/?a=1&b=2" {
		t.Fatalf("分组「常用 & 工具」不符: %+v", bs)
	}
	if bs := byName["中文分组"]; len(bs) != 1 || bs[0].URL != "https://example.com/中文" {
		t.Fatalf("分组「中文分组」不符: %+v", byName)
	}

	res, err = svc.ImportBookmarksHTML(strings.NewReader(string(out)))
	must(t, err)
	if res.Created != 0 || res.Skipped != 3 || res.GroupsCreated != 0 {
		t.Fatalf("重复导入应全部跳过: %+v", res)
	}

	// 嵌套文件夹拍平后的名字超过 varchar(100)：必须按 rune 截断，
	// 按字节切会切碎汉字，PostgreSQL 直接拒收整个事务。
	long := strings.Repeat("长", 60)
	nested := `<DL><p><DT><H3>` + long + `</H3><DL><p><DT><H3>` + long + `</H3><DL><p>
		<DT><A HREF="https://deep.example/">deep</A>
		<DT><A HREF="ftp://deep.example/">ftp</A>
		</DL><p></DL><p></DL><p>`
	res, err = svc.ImportBookmarksHTML(strings.NewReader(nested))
	must(t, err)
	if res.Created != 1 || res.Skipped != 1 || res.GroupsCreated != 1 {
		t.Fatalf("嵌套导入结果不符: %+v", res)
	}
	want := model.TruncateRunes(long+"/"+long, 100)
	var n int64
	db.Model(&model.BookmarkGroup{}).Where("name = ?", want).Count(&n)
	if n != 1 {
		t.Fatalf("应存在截断到 100 rune 的分组 %q", want)
	}
}

// TestParseNetscapeBookmarksRejectsDeepNesting 防的是构造文件耗尽内存。
//
// 文件夹栈曾没有深度上限，且每条书签都把整条路径重新拼一遍、最后才截断：
// 几十万层不闭合的 <H3>a</H3><DL> 加上两万个链接就能分配十几 GB。
// 不带标题的 <DL> 同样压栈，必须一起计入深度，否则换个写法就绕过了。
func TestParseNetscapeBookmarksRejectsDeepNesting(t *testing.T) {
	cases := map[string]string{
		"titled":   "<DL><p>" + strings.Repeat("<DT><H3>a</H3><DL><p>", maxBookmarkFolderDepth+1),
		"untitled": "<DL><p>" + strings.Repeat("<DL><p>", maxBookmarkFolderDepth+1),
	}
	for name, folders := range cases {
		t.Run(name, func(t *testing.T) {
			src := folders + `<DT><A HREF="https://e.example/">x</A>`
			_, err := parseNetscapeBookmarks(strings.NewReader(src))
			if !errors.Is(err, ErrBookmarkImportTooDeep) {
				t.Fatalf("期望 ErrBookmarkImportTooDeep，got %v", err)
			}
		})
	}
}

// TestParseNetscapeBookmarksAllowsMaxDepth 确认上限本身是可用的，
// 正常的深层收藏夹不会被误拒。
func TestParseNetscapeBookmarksAllowsMaxDepth(t *testing.T) {
	src := "<DL><p>" + strings.Repeat("<DT><H3>a</H3><DL><p>", maxBookmarkFolderDepth) +
		`<DT><A HREF="https://e.example/">x</A>`
	got, err := parseNetscapeBookmarks(strings.NewReader(src))
	if err != nil {
		t.Fatalf("恰好 %d 层不应失败: %v", maxBookmarkFolderDepth, err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d 条", len(got))
	}
}

// TestParseNetscapeBookmarksTruncatesGroupNameToColumn 确认分组名在解析阶段
// 就截断到列宽，且与「先拼全路径再截断」的结果一致。
//
// 截断提前到压栈时做，单条书签的分组名就不会随层数和名字长度膨胀。
func TestParseNetscapeBookmarksTruncatesGroupNameToColumn(t *testing.T) {
	long := strings.Repeat("文", 60)
	src := "<DL><p>" + strings.Repeat("<DT><H3>"+long+"</H3><DL><p>", 5) +
		`<DT><A HREF="https://e.example/">x</A></DL><p><DT><A HREF="https://f.example/">y</A>`
	got, err := parseNetscapeBookmarks(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	full := strings.Join([]string{long, long, long, long, long}, bookmarkFolderSep)
	if want := model.TruncateRunes(full, bookmarkGroupNameMaxRunes); got[0].group != want {
		t.Errorf("5 层分组名 = %q，期望 %q", got[0].group, want)
	}
	// 弹栈后回到上一层，名字也必须回退，而不是沿用截断后的深层名字。
	full4 := strings.Join([]string{long, long, long, long}, bookmarkFolderSep)
	if want := model.TruncateRunes(full4, bookmarkGroupNameMaxRunes); got[1].group != want {
		t.Errorf("弹栈后分组名 = %q，期望 %q", got[1].group, want)
	}
}

// TestBookmarkGroupNameMaxMatchesColumn 锁住常量与 GORM 列宽一致；
// 改了列宽而忘了常量，导入要么截得过短，要么被 PostgreSQL 拒收。
func TestBookmarkGroupNameMaxMatchesColumn(t *testing.T) {
	sch, err := schema.Parse(&model.BookmarkGroup{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	f := sch.LookUpField("Name")
	if f == nil || f.Size != bookmarkGroupNameMaxRunes {
		t.Fatalf("BookmarkGroup.Name size = %v，常量 = %d", f, bookmarkGroupNameMaxRunes)
	}
}
