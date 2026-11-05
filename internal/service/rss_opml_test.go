package service

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 导出结构：分组嵌套一级、未分组的在顶层、订阅源带 type/xmlUrl/htmlUrl。
// 防的是阅读器导不进去——Feedly/Inoreader 只认 xmlUrl 与 type="rss"。
func TestExportOPMLNestsFeedsUnderGroups(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "rss_groups"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "deleted_at", "name", "sort_order"}).
			AddRow(1, now, now, nil, "Tech & News", 0))
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "url", "site_url", "group_id"}).
			AddRow(10, "Go Blog", "https://go.dev/blog/feed.atom", "https://go.dev/blog", 1).
			AddRow(11, "", "https://example.com/rss?a=1&b=2", "", nil).
			AddRow(12, "Orphan", "https://orphan.example/feed", "", 99))

	svc := &RSSService{db: db}
	out, err := svc.ExportOPML()
	if err != nil {
		t.Fatalf("ExportOPML 失败: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, `<?xml version="1.0" encoding="UTF-8"?>`) || !strings.Contains(s, `<opml version="2.0">`) {
		t.Fatalf("缺少 XML 声明或 OPML 版本:\n%s", s)
	}
	// & 必须被转义，否则文件不是合法 XML。
	if !strings.Contains(s, `xmlUrl="https://example.com/rss?a=1&amp;b=2"`) {
		t.Errorf("URL 中的 & 未转义:\n%s", s)
	}

	var doc opmlDoc
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("导出结果不是合法 XML: %v", err)
	}
	body := doc.Body.Outlines
	if len(body) != 3 {
		t.Fatalf("顶层 outline 数 = %d，期望 1 个分组 + 2 个未分组源:\n%s", len(body), s)
	}
	g := body[0]
	if g.Text != "Tech & News" || g.XMLURL != "" || len(g.Outlines) != 1 {
		t.Fatalf("分组 outline 不符: %+v", g)
	}
	f := g.Outlines[0]
	if f.Type != "rss" || f.XMLURL != "https://go.dev/blog/feed.atom" || f.HTMLURL != "https://go.dev/blog" || f.Text != "Go Blog" || f.Title != "Go Blog" {
		t.Errorf("订阅源 outline 字段不符: %+v", f)
	}
	// 标题为空时 text 退回 URL（text 是 OPML 必填属性）。
	if body[1].Text != "https://example.com/rss?a=1&b=2" {
		t.Errorf("无标题源的 text = %q", body[1].Text)
	}
	// 分组已删除的源落到顶层，而不是丢失。
	if body[2].XMLURL != "https://orphan.example/feed" {
		t.Errorf("分组不存在的源应放顶层: %+v", body[2])
	}
}

// 任意深度的嵌套：分组名取最近一级带子节点的 outline。
func TestFlattenOPMLUsesNearestFolder(t *testing.T) {
	src := `<opml version="2.0"><body>
		<outline text="Top" type="rss" xmlUrl="https://top/feed"/>
		<outline text="Outer">
			<outline text="A" xmlUrl="https://a/feed"/>
			<outline text="Inner">
				<outline title="B" xmlUrl="https://b/feed"/>
			</outline>
		</outline>
	</body></opml>`
	var doc opmlDoc
	if err := xml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	var got []opmlFeed
	if err := flattenOPML(doc.Body.Outlines, "", &got); err != nil {
		t.Fatal(err)
	}
	want := []opmlFeed{
		{"https://top/feed", "Top", ""},
		{"https://a/feed", "A", "Outer"},
		{"https://b/feed", "B", "Inner"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// 条数上限：超出时整次拒绝，而不是悄悄只导入前 2000 条。
func TestFlattenOPMLRejectsTooManyFeeds(t *testing.T) {
	outlines := make([]opmlOutline, maxOPMLFeeds+1)
	for i := range outlines {
		outlines[i] = opmlOutline{XMLURL: fmt.Sprintf("https://x/%d", i)}
	}
	var got []opmlFeed
	if err := flattenOPML(outlines, "", &got); !errors.Is(err, ErrOPMLTooManyURL) {
		t.Fatalf("期望 ErrOPMLTooManyURL，got %v", err)
	}
}

// 大小上限在读入时就生效，不先把整个文件读进内存再判断。
func TestImportOPMLRejectsOversizedFile(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := &RSSService{db: db}
	big := strings.NewReader(strings.Repeat(" ", MaxOPMLBytes+1))
	if _, err := svc.ImportOPML(big); !errors.Is(err, ErrOPMLTooLarge) {
		t.Fatalf("期望 ErrOPMLTooLarge，got %v", err)
	}
	if _, err := svc.ImportOPML(strings.NewReader("<not-xml")); !errors.Is(err, ErrOPMLInvalid) {
		t.Fatalf("期望 ErrOPMLInvalid，got %v", err)
	}
}

// 真实数据库：去重、分组创建、URL 校验，以及导出能被导回来。
func TestIntegrationOPMLImportDedupesAndCreatesGroups(t *testing.T) {
	db := stateIntegrationDB(t)
	svc := &RSSService{db: db}

	existingGroup := model.RSSGroup{Name: "Existing"}
	must(t, db.Create(&existingGroup).Error)
	must(t, db.Create(&model.RSSFeed{Title: "old", URL: "https://old.example/feed"}).Error)

	src := `<?xml version="1.0"?><opml version="2.0"><head><title>t</title></head><body>
		<outline text="Existing">
			<outline text="Old" type="rss" xmlUrl="https://old.example/feed"/>
			<outline text="N1" type="rss" xmlUrl="https://n1.example/feed"/>
		</outline>
		<outline text="Fresh">
			<outline text="N2" type="rss" xmlUrl="https://n2.example/feed"/>
			<outline text="N1 again" type="rss" xmlUrl="https://n1.example/feed"/>
			<outline text="Bad" type="rss" xmlUrl="http://127.0.0.1/feed"/>
		</outline>
		<outline text="Loose" type="rss" xmlUrl="https://loose.example/feed"/>
	</body></opml>`
	res, err := svc.ImportOPML(strings.NewReader(src))
	must(t, err)
	if res.Created != 3 || res.Skipped != 2 || res.GroupsCreated != 1 || len(res.Errors) != 1 || res.Errors[0].URL != "http://127.0.0.1/feed" {
		t.Fatalf("导入结果不符: %+v", res)
	}

	var n1 model.RSSFeed
	must(t, db.Where("url = ?", "https://n1.example/feed").First(&n1).Error)
	if n1.GroupID == nil || *n1.GroupID != existingGroup.ID || n1.Title != "N1" {
		t.Errorf("N1 应进已有分组 Existing: %+v", n1)
	}
	var loose model.RSSFeed
	must(t, db.Where("url = ?", "https://loose.example/feed").First(&loose).Error)
	if loose.GroupID != nil {
		t.Errorf("顶层源不应有分组: %+v", loose)
	}

	// 再导一次：全部跳过，不重复建分组。
	res, err = svc.ImportOPML(strings.NewReader(src))
	must(t, err)
	if res.Created != 0 || res.GroupsCreated != 0 || res.Skipped != 5 {
		t.Fatalf("重复导入应全部跳过: %+v", res)
	}

	// 导出 → 解析：所有源都在。
	out, err := svc.ExportOPML()
	must(t, err)
	var doc opmlDoc
	must(t, xml.Unmarshal(out, &doc))
	var flat []opmlFeed
	must(t, flattenOPML(doc.Body.Outlines, "", &flat))
	if len(flat) != 4 {
		t.Fatalf("导出应含 4 个源，got %d: %s", len(flat), out)
	}
}
