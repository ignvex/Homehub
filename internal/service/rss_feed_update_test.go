package service

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
	"github.com/mmcdole/gofeed"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func uint64Ptr(v uint64) *uint64 { return &v }

// TestBuildFeedUpdatesDistinguishesClearFromSkip 是这次回归的语义核心。
//
// 前端选「未分组」时把 group_id 置空，但 `*uint64` 在 JSON 缺失和显式 null
// 两种情况下都是 nil，service 只能判断"没改"。结果是用户永远无法把一个
// 订阅源从分组里移出来 —— 界面上点了没用，也不报错。
//
// GroupUpdate 把"没带这个字段"和"带了这个字段但值为空"分开表达：
// 前者跳过，后者写入 NULL。
func TestBuildFeedUpdatesDistinguishesClearFromSkip(t *testing.T) {
	cases := []struct {
		name    string
		group   GroupUpdate
		wantKey bool
		wantNil bool
		wantVal uint64
	}{
		{name: "字段缺失则不动分组", group: GroupUpdate{}, wantKey: false},
		{name: "显式 null 清空分组", group: GroupUpdate{Set: true, Value: nil}, wantKey: true, wantNil: true},
		{name: "具体值写入分组", group: GroupUpdate{Set: true, Value: uint64Ptr(7)}, wantKey: true, wantVal: 7},
		// Set=false 时即便带了值也不应写入：这是调用方的误用，按"不修改"处理更安全。
		{name: "未声明 Set 时忽略值", group: GroupUpdate{Set: false, Value: uint64Ptr(9)}, wantKey: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updates, err := buildFeedUpdates(FeedUpdate{Group: tc.group})
			if err != nil {
				t.Fatalf("构造更新失败: %v", err)
			}
			value, ok := updates["group_id"]
			if ok != tc.wantKey {
				t.Fatalf("group_id 是否出现在 updates 中 = %v，期望 %v（updates=%v）", ok, tc.wantKey, updates)
			}
			if !ok {
				return
			}
			if tc.wantNil {
				if value != nil {
					t.Errorf("group_id = %v，期望 nil（写入 NULL）", value)
				}
				return
			}
			got, isUint := value.(uint64)
			if !isUint {
				t.Fatalf("group_id 类型 = %T，期望 uint64", value)
			}
			if got != tc.wantVal {
				t.Errorf("group_id = %d，期望 %d", got, tc.wantVal)
			}
		})
	}
}

// TestClearedGroupBecomesSQLNull 确认清除意图真的落成 SQL 里的 NULL。
//
// map 里放了 nil 还不够：GORM 究竟把它写成参数化 NULL、字面量 NULL，
// 还是干脆从 SET 子句剔除，决定了数据库里的分组是否真的被清空。
// 这里用 DryRun 拿到真实语句逐项核对。
func TestClearedGroupBecomesSQLNull(t *testing.T) {
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

	tx := db.Model(&model.RSSFeed{}).Where("id = ?", 42).
		Updates(func() map[string]interface{} {
			u, err := buildFeedUpdates(FeedUpdate{Group: GroupUpdate{Set: true, Value: nil}})
			if err != nil {
				t.Fatalf("构造更新失败: %v", err)
			}
			return u
		}())
	if tx.Error != nil {
		t.Fatalf("生成 SQL 失败: %v", tx.Error)
	}

	sql := tx.Statement.SQL.String()
	if !strings.Contains(sql, `"group_id"`) {
		t.Fatalf("SET 子句未包含 group_id，清除不会生效: %s", sql)
	}
	// 只挑出 group_id 对应的占位符位置，避免依赖 updated_at 的位置。
	idx := strings.Index(sql, `"group_id"=$`)
	if idx < 0 {
		t.Fatalf("group_id 未参数化: %s", sql)
	}
	placeholder := sql[idx+len(`"group_id"=$`)]
	paramIndex := int(placeholder - '1')
	if paramIndex < 0 || paramIndex >= len(tx.Statement.Vars) {
		t.Fatalf("占位符 $%c 超出参数列表长度 %d: %s", placeholder, len(tx.Statement.Vars), sql)
	}
	if tx.Statement.Vars[paramIndex] != nil {
		t.Errorf("group_id 参数 = %v，期望 nil（对应 SQL NULL）", tx.Statement.Vars[paramIndex])
	}
}

// intPtr 返回 int 指针。
func intPtr(v int) *int { return &v }

// TestUpdateClearsRemarkAndTitle 确认清空是有意义、且被支持的意图。
//
// 旧的 buildFeedUpdates 用「零值跳过」，导致用户删空备注后再也存不回去：
// 请求发了、返回成功、值还在。改成指针判空之后，"空串"才真正表示清空。
func TestUpdateClearsRemarkAndTitle(t *testing.T) {
	empty := ""
	updates, err := buildFeedUpdates(FeedUpdate{Title: &empty, Remark: &empty})
	if err != nil {
		t.Fatalf("构造更新失败: %v", err)
	}

	if v, ok := updates["remark"]; !ok || v != "" {
		t.Fatalf("remark 应被显式写成空串，实际 updates=%v", updates)
	}
	if v, ok := updates["title"]; !ok || v != "" {
		t.Fatalf("title 应被显式写成空串，实际 updates=%v", updates)
	}
}

// TestUpdateOmitsFieldsThatWereNotSent 确认「没发」不等于「清空」。
func TestUpdateOmitsFieldsThatWereNotSent(t *testing.T) {
	updates, err := buildFeedUpdates(FeedUpdate{Group: GroupUpdate{Set: false}})
	if err != nil {
		t.Fatalf("构造更新失败: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("没有更新意图时不应产生 SET 子句，实际 %v", updates)
	}
}

// TestUpdateRejectsInvalidFetchBy 确认执行者取值受校验。
//
// 认不出的值不会被拒绝的话，会在 fetch_by 列里留下脏数据，
// 而 FetchAll 的判定会把它当成 local（NormalizeFetchBy 只兜空串），
// 结果是「设成远端却在本地抓」。
func TestUpdateRejectsInvalidFetchBy(t *testing.T) {
	bad := "cloud"
	if _, err := buildFeedUpdates(FeedUpdate{FetchBy: &bad}); err == nil {
		t.Fatal("非法 fetch_by 必须被拒绝")
	}
}

func TestUpdateRejectsNonPositiveInterval(t *testing.T) {
	zero := 0
	if _, err := buildFeedUpdates(FeedUpdate{FetchInterval: &zero}); err == nil {
		t.Fatal("间隔必须大于 0")
	}
}

// TestUpdateAcceptsBothFetchByValues 确认两个合法值都能写入。
func TestUpdateAcceptsBothFetchByValues(t *testing.T) {
	for _, v := range model.FetchByValues() {
		value := v
		updates, err := buildFeedUpdates(FeedUpdate{FetchBy: &value})
		if err != nil {
			t.Fatalf("%s 应被接受: %v", v, err)
		}
		if updates["fetch_by"] != v {
			t.Fatalf("fetch_by 应为 %s，实际 %v", v, updates["fetch_by"])
		}
	}
}

// TestShouldAdoptTitleOnlyFillsBlanks 锁住「抓取不覆盖用户标题」。
//
// 用户手动起标题通常正是因为 feed 的原始标题没法看（SEO 堆关键词、
// 带站点后缀、纯英文）。旧的判定是「不同就写」，第一次抓取就把
// 改过的名字冲掉了。现在的规则是：本地为空才补全。
func TestShouldAdoptTitleOnlyFillsBlanks(t *testing.T) {
	cases := []struct {
		name   string
		local  string
		parsed string
		want   bool
	}{
		{"本地为空时补全", "", "原始标题", true},
		{"本地只有空白也算空", "   ", "原始标题", true},
		{"用户填过就不覆盖", "我起的名字", "原始标题", false},
		{"完全相同也不必写", "同一个", "同一个", false},
		{"feed 没给标题时不覆盖为空", "我起的名字", "", false},
		{"feed 给了空白标题也不写", "我起的名字", "  ", false},
		{"两边都空则不写", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAdoptTitle(tc.local, tc.parsed); got != tc.want {
				t.Fatalf("shouldAdoptTitle(%q, %q) = %v，期望 %v", tc.local, tc.parsed, got, tc.want)
			}
		})
	}
}

// TestCreateFeedDoesNotFetch 确认保存路径不发起网络请求。
//
// 旧实现同步抓取解析，导致「网络不通就存不了订阅」——而很多源在添加
// 时临时不可达，或者要等代理就位才抓得动。把一个纯写入操作绑上网络
// 条件，失败原因会指向错误的方向。
func TestCreateFeedDoesNotFetch(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	fetcher := &countingFetcher{}
	mock.ExpectQuery(`SELECT count\(\*\) FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	svc := NewRSSService(db)
	svc.SetFetcher(fetcher)

	remark := "测试备注"
	title := "我填的标题"
	interval := 1800
	feed, err := svc.CreateFeed(CreateFeedInput{
		URL:           "https://example.com/feed.xml",
		Title:         &title,
		Remark:        &remark,
		FetchInterval: &interval,
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("创建时不应抓取，实际调用了 %d 次", fetcher.calls)
	}
	if feed.Remark != remark {
		t.Fatalf("备注应写入，实际 %q", feed.Remark)
	}
	if feed.Title != title {
		t.Fatalf("标题应保留用户填的值，实际 %q", feed.Title)
	}
	if feed.FetchInterval != interval {
		t.Fatalf("间隔应写入，实际 %d", feed.FetchInterval)
	}
	if feed.FetchBy != model.FetchByLocal {
		t.Fatalf("未指定 fetch_by 时应默认 local，实际 %q", feed.FetchBy)
	}
}

// countingFetcher 只数调用次数。
type countingFetcher struct{ calls int }

func (c *countingFetcher) Fetch(ctx context.Context, url string) (*gofeed.Feed, error) {
	c.calls++
	return &gofeed.Feed{Title: "不该被用到"}, nil
}
