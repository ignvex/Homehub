package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ignvex/HomeHub/internal/testutil"
)

// rssFeedColumns 与 RSSFeed 的列一一对应。
func rssFeedColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "deleted_at",
		"title", "url", "site_url", "description", "icon_url",
		"fetch_interval", "last_fetch", "error_msg", "group_id",
	}
}

// expectFeedByID 安排一次按主键读订阅源的查询。
func expectFeedByID(mock sqlmock.Sqlmock, id uint64, title, url string, interval int) {
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WithArgs(id, 1).
		WillReturnRows(sqlmock.NewRows(rssFeedColumns()).
			AddRow(id, time.Now(), time.Now(), nil, title, url, "", "", "", interval, time.Time{}, "", nil))
}

// captureUpsert 起一个假 Extension，把收到的推送请求体解出来。
//
// 只实现这一个端点：本测试关心的是「HomeHub 发出去的 body 长什么样」，
// 其余端点不在断言范围内。
func captureUpsert(t *testing.T, into *hubUpsertBody) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, into); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             42,
			"url":            into.URL,
			"title":          into.Title,
			"enabled":        true,
			"fetch_interval": "30m",
			"created_at":     time.Now().Unix(),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// hubUpsertBody 是推送请求体的探针。
//
// 字段都用指针/两次解包来区分「没发这个键」与「发了零值」——
// 协议里这两者语义相反，用普通字段会把它们混成一件事。
type hubUpsertBody struct {
	URL      string  `json:"url"`
	Title    *string `json:"title"`
	Interval *string `json:"interval"`
	Enabled  *bool   `json:"enabled"`
	FetchNow *bool   `json:"fetch_now"`
}

func TestPushFeedSendsMappedFieldsFromDatabase(t *testing.T) {
	// 从库到请求体是一条完整的映射链：本地标题、抓取间隔、启停状态
	// 都要按协议的三态语义发出去。这条测试抓的是映射链上的回归。
	var captured hubUpsertBody
	upstream := captureUpsert(t, &captured)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	keyPath, _ := newTestSignerFile(t)
	expectFeedByID(mock, 7, "Example Blog", "https://example.com/feed.xml", 1800)

	svc := newTestHubService(t, upstream.URL+"/api/v1", keyPath)
	svc.db = db

	result, err := svc.PushFeed(context.Background(), 7, nil, true)
	if err != nil {
		t.Fatalf("推送失败: %v", err)
	}
	if !result.Created || result.RemoteID != 42 {
		t.Fatalf("结果不对: %+v", result)
	}

	if captured.URL != "https://example.com/feed.xml" {
		t.Fatalf("url = %q", captured.URL)
	}
	if captured.Title == nil || *captured.Title != "Example Blog" {
		t.Fatalf("title 应原样传递，实际 %v", captured.Title)
	}
	if captured.Interval == nil || *captured.Interval != "30m" {
		t.Fatalf("interval 应为 30m，实际 %v", captured.Interval)
	}
	if captured.Enabled != nil {
		t.Fatalf("未指定 enabled 时不应发送，实际 %v", *captured.Enabled)
	}
	if captured.FetchNow == nil || !*captured.FetchNow {
		t.Fatal("手动推送应显式 fetch_now=true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

func TestPushFeedPassesExplicitEnabledToRestoreFeed(t *testing.T) {
	// 恢复被自动停用的源必须显式传 enabled=true：省略只会保持
	// 对方现有的 false，用户点了“恢复”却什么也不会发生。
	var captured hubUpsertBody
	upstream := captureUpsert(t, &captured)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	keyPath, _ := newTestSignerFile(t)
	expectFeedByID(mock, 3, "Feed", "https://example.com/feed.xml", 3600)

	svc := newTestHubService(t, upstream.URL+"/api/v1", keyPath)
	svc.db = db

	enabled := true
	if _, err := svc.PushFeed(context.Background(), 3, &enabled, false); err != nil {
		t.Fatalf("推送失败: %v", err)
	}
	if captured.Enabled == nil || !*captured.Enabled {
		t.Fatalf("enabled=true 必须透传，实际 %v", captured.Enabled)
	}
	if captured.FetchNow == nil || *captured.FetchNow {
		t.Fatal("fetch_now=false 必须显式发送")
	}
}

func TestPushFeedReportsMissingLocalFeed(t *testing.T) {
	// 本地没有这条订阅时要给 404，而不是把空 URL 推上去——
	// 后者会被 Extension 以 400 拒绝，错误指向错误的方向。
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	keyPath, _ := newTestSignerFile(t)
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WithArgs(uint64(99), 1).
		WillReturnRows(sqlmock.NewRows(rssFeedColumns()))

	svc := newTestHubService(t, "http://127.0.0.1:1/api/v1", keyPath)
	svc.db = db

	_, err := svc.PushFeed(context.Background(), 99, nil, true)
	if err == nil {
		t.Fatal("不存在的订阅源必须报错")
	}
	if !errors.Is(err, ErrRSSFeedNotFound) {
		t.Fatalf("错误应可识别为订阅源不存在，实际 %v", err)
	}
}

// isFeedNotFound 判断错误是否为 ErrRSSFeedNotFound。
func TestSyncAllPushesEveryFeedWithoutDeleting(t *testing.T) {
	// 对账只推不删：删除会连带清掉 Extension 已抓取的条目，
	// 必须由用户逐条确认。这条测试同时锁住“删除请求一次都不发”。
	var pushes int
	var deletes int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			pushes++
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": uint64(pushes), "url": "x", "enabled": true,
				"fetch_interval": "1h", "created_at": time.Now().Unix(),
			})
		case http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		}
	}))
	defer upstream.Close()

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(rssFeedColumns()).
			AddRow(1, time.Now(), time.Now(), nil, "A", "https://a.example/feed", "", "", "", 3600, time.Time{}, "", nil).
			AddRow(2, time.Now(), time.Now(), nil, "B", "https://b.example/feed", "", "", "", 1800, time.Time{}, "", nil))

	keyPath, _ := newTestSignerFile(t)
	svc := newTestHubService(t, upstream.URL+"/api/v1", keyPath)
	svc.db = db

	result, err := svc.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if result.Total != 2 || result.Updated != 2 || result.Failed != 0 {
		t.Fatalf("统计不对: %+v", result)
	}
	if pushes != 2 {
		t.Fatalf("应推送 2 条，实际 %d", pushes)
	}
	if deletes != 0 {
		t.Fatalf("对账绝不能删除远端订阅，实际发了 %d 次 DELETE", deletes)
	}
}
