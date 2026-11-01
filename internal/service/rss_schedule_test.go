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
		"fetch_interval", "fetch_by", "last_fetch", "error_msg", "group_id",
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
				3600, model.FetchByLocal, now.Add(-2*time.Hour), "", nil).
			AddRow(2, now, now, nil, "远端源", "https://b.example/f", "", "", "", "",
				3600, model.FetchByExtension, now.Add(-2*time.Hour), "", nil))

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
				3600, model.FetchByLocal, time.Time{}, "", nil))

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
				3600, model.FetchByLocal, time.Now().Add(-2*time.Hour), "", nil))

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
				1800, model.FetchByLocal, last, "", nil))

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
