package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// loadScheduleConfig 写一份 RSS 段可控的最小配置。
func loadScheduleConfig(t *testing.T, backgroundEnabled bool) {
	t.Helper()

	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "error", "format": "json"},
		"rss": {"background_fetch_enabled": ` + map[bool]string{true: "true", false: "false"}[backgroundEnabled] + `, "background_fetch_interval": 300}
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
}

// scheduleFeedColumns 与 RSSFeed 的列按 Order("id ASC") 查询一致。
func scheduleFeedColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "deleted_at",
		"title", "url", "site_url", "description", "remark", "icon_url",
		"fetch_interval", "fetch_by", "fetch_enabled", "last_fetch", "error_msg", "group_id",
	}
}

func TestScheduleSkipsExtensionFeeds(t *testing.T) {
	// 交给 Extension 的源不参与本机调度。把它算成「本机待抓」会让人
	// 以为 HomeHub 在抓，实际抓的是对面——而且两边各存一份条目。
	loadScheduleConfig(t, true)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, now, now, nil, "本机源", "https://a.example/f", "", "", "", "",
				3600, model.FetchByLocal, true, now.Add(-2*time.Hour), "", nil).
			AddRow(2, now, now, nil, "远端源", "https://b.example/f", "", "", "", "",
				3600, model.FetchByExtension, true, now.Add(-2*time.Hour), "", nil))

	state, err := NewRSSService(db).Schedule()
	if err != nil {
		t.Fatalf("查询调度失败: %v", err)
	}

	if state.LocalCount != 1 || state.ExtensionCount != 1 {
		t.Fatalf("执行者分布不对: local=%d extension=%d", state.LocalCount, state.ExtensionCount)
	}
	if state.DueCount != 1 {
		t.Fatalf("只有本机的那个源算到期，实际 due_count=%d", state.DueCount)
	}

	for _, f := range state.Feeds {
		if f.FeedID == 2 {
			if f.Enabled {
				t.Fatal("交给 Extension 的源不应显示为「启用中」")
			}
		}
	}
}

func TestScheduleCountsNeverFetchedLocalFeedAsDue(t *testing.T) {
	// 从没抓过的本机源一旦总开关打开就该被抓。
	// 这与 FetchAll 的判定一致（LastFetch 为零值时 now.Sub 大于任何间隔）。
	loadScheduleConfig(t, true)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, time.Now(), time.Now(), nil, "", "https://a.example/f", "", "", "", "",
				3600, model.FetchByLocal, true, time.Time{}, "", nil))

	state, err := NewRSSService(db).Schedule()
	if err != nil {
		t.Fatalf("查询调度失败: %v", err)
	}
	if state.DueCount != 1 {
		t.Fatalf("从未抓取的本机源应算到期，实际 due_count=%d", state.DueCount)
	}
	if !state.Feeds[0].Due {
		t.Fatal("due 应为 true")
	}
	if state.Feeds[0].LastFetch != nil {
		t.Fatal("从未抓取时 last_fetch_at 应为 null")
	}
}

func TestScheduleReportsDisabledMasterSwitch(t *testing.T) {
	// 总开关关闭时，即使源已到期也不该算「待抓」——否则界面会显示
	// 一堆待办，而实际上什么都不会发生。
	loadScheduleConfig(t, false)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, time.Now(), time.Now(), nil, "", "https://a.example/f", "", "", "", "",
				3600, model.FetchByLocal, true, time.Now().Add(-2*time.Hour), "", nil))

	state, err := NewRSSService(db).Schedule()
	if err != nil {
		t.Fatalf("查询调度失败: %v", err)
	}
	if state.Enabled {
		t.Fatal("总开关关闭时 enabled 应为 false")
	}
	if state.DueCount != 0 {
		t.Fatalf("总开关关闭时不应计入待抓，实际 %d", state.DueCount)
	}
}

func TestScheduleComputesNextFetchFromInterval(t *testing.T) {
	loadScheduleConfig(t, true)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	last := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, time.Now(), time.Now(), nil, "", "https://a.example/f", "", "", "", "",
				1800, model.FetchByLocal, true, last, "", nil))

	state, err := NewRSSService(db).Schedule()
	if err != nil {
		t.Fatalf("查询调度失败: %v", err)
	}

	f := state.Feeds[0]
	if f.NextFetchAt == nil {
		t.Fatal("应给出下次抓取时间")
	}
	want := last.Add(1800 * time.Second).Unix()
	if *f.NextFetchAt != want {
		t.Fatalf("下次抓取 = %d，期望 %d", *f.NextFetchAt, want)
	}
	if f.Due {
		t.Fatal("刚过 10 分钟、间隔 30 分钟的源不该到期")
	}
}

// TestScheduleMarksPausedFeedSeparately 确认「手动暂停」与「总开关关闭」
// 是两个可区分的状态。
//
// 压成一个布尔值的话，界面只能显示「不会抓」，用户分不清是自己停的
// 还是系统没开——前者去点恢复，后者去改配置，修法完全不同。
func TestScheduleMarksPausedFeedSeparately(t *testing.T) {
	loadScheduleConfig(t, true)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, now, now, nil, "暂停的", "https://a.example/f", "", "", "", "",
				3600, model.FetchByLocal, false, now.Add(-2*time.Hour), "", nil).
			AddRow(2, now, now, nil, "正常的", "https://b.example/f", "", "", "", "",
				3600, model.FetchByLocal, true, now.Add(-2*time.Hour), "", nil))

	state, err := NewRSSService(db).Schedule()
	if err != nil {
		t.Fatalf("查询调度失败: %v", err)
	}

	byID := map[uint64]FeedSchedule{}
	for _, f := range state.Feeds {
		byID[f.FeedID] = f
	}

	paused := byID[1]
	if paused.FetchEnabled {
		t.Fatal("fetch_enabled 应为 false")
	}
	if paused.Enabled {
		t.Fatal("被暂停的源不应算作「启用中」")
	}
	// 到期仍然要算出来：暂停不等于「不欠账」，恢复后应当立刻补抓。
	if !paused.Due {
		t.Fatal("被暂停但已过期的源仍应标记 due，恢复后才能立即补抓")
	}

	if !byID[2].Enabled {
		t.Fatal("未暂停的源应为启用中")
	}
	if state.DueCount != 1 {
		t.Fatalf("只有未暂停的那个计入待抓，实际 %d", state.DueCount)
	}
}

// TestUpdateFeedTogglesFetchEnabled 确认暂停/恢复只写这一列。
func TestUpdateFeedTogglesFetchEnabled(t *testing.T) {
	paused := false
	updates, err := buildFeedUpdates(FeedUpdate{FetchEnabled: &paused})
	if err != nil {
		t.Fatalf("构造更新失败: %v", err)
	}
	if v, ok := updates["fetch_enabled"]; !ok || v != false {
		t.Fatalf("fetch_enabled 应被写成 false，实际 %v", updates)
	}

	resumed := true
	updates, err = buildFeedUpdates(FeedUpdate{FetchEnabled: &resumed})
	if err != nil {
		t.Fatalf("构造更新失败: %v", err)
	}
	if v, ok := updates["fetch_enabled"]; !ok || v != true {
		t.Fatalf("fetch_enabled 应被写成 true，实际 %v", updates)
	}
}

// TestCreateFeedDefaultsToEnabled 确认新建默认参与调度。
//
// 默认关闭的话，用户添加完源却发现一直不更新，只会以为是抓取坏了。
func TestCreateFeedDefaultsToEnabled(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	svc := NewRSSService(db)
	svc.SetFetcher(&countingFetcher{})

	feed, err := svc.CreateFeed(CreateFeedInput{URL: "https://example.com/feed.xml"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if !feed.FetchEnabled {
		t.Fatal("新建的源默认应参与调度")
	}
}

// TestCreateFeedCanStartPaused 确认「创建时就暂停」真的落库。
//
// GORM 对带 `default:true` 的字段会在值为零值时从 INSERT 里剔除该列，
// 让数据库用默认值 —— 于是显式传来的 false 被静默改成 true。
// 实测确认 Select("*")、BeforeCreate+SetColumn 都拦不住，只能补一次
// 显式 UPDATE。这条测试盯的就是那次 UPDATE 有没有发出去。
func TestCreateFeedCanStartPaused(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	// 关键断言：必须有一条显式写入 false 的 UPDATE。
	// 测试库关闭了默认事务，所以没有 Begin/Commit。
	mock.ExpectExec(`UPDATE "rss_feeds" SET "fetch_enabled"=\$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	svc := NewRSSService(db)
	svc.SetFetcher(&countingFetcher{})

	paused := false
	if _, err := svc.CreateFeed(CreateFeedInput{
		URL:          "https://example.com/feed.xml",
		FetchEnabled: &paused,
	}); err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("暂停意图没有被写入: %v", err)
	}
}

// TestCreateFeedSkipsUpdateWhenEnabled 确认默认路径不多发一次 UPDATE。
func TestCreateFeedSkipsUpdateWhenEnabled(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	svc := NewRSSService(db)
	svc.SetFetcher(&countingFetcher{})

	if _, err := svc.CreateFeed(CreateFeedInput{URL: "https://example.com/feed.xml"}); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("默认启用时不应额外 UPDATE: %v", err)
	}
}
