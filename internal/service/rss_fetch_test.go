package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
	"github.com/mmcdole/gofeed"
)

// fakeFetcher 是可控的 RSSFetcher 测试替身。
type fakeFetcher struct {
	calls   atomic.Int32
	delay   time.Duration
	delayBy map[string]time.Duration
	failFor map[string]bool
}

func (f *fakeFetcher) Fetch(ctx context.Context, url string) (*gofeed.Feed, error) {
	f.calls.Add(1)

	wait := f.delay
	if d, ok := f.delayBy[url]; ok {
		wait = d
	}
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.failFor[url] {
		return nil, fmt.Errorf("模拟抓取失败: %s", url)
	}
	return &gofeed.Feed{
		Title: "test",
		Items: []*gofeed.Item{{Title: "item", Link: url + "/item"}},
	}, nil
}

// withTempCacheDir 把图片缓存目录指向临时目录，并在测试结束后还原。
func withTempCacheDir(t *testing.T) string {
	t.Helper()
	bak := cacheDir
	dir := t.TempDir()
	cacheDir = dir
	t.Cleanup(func() { cacheDir = bak })
	return dir
}

// TestSanitizeContentImageCacheDisabled 确认关闭图片缓存后保留原始外链。
//
// 这是"抓取变快"的主要开关：源站图片可直连时无需本地中转。
func TestSanitizeContentImageCacheDisabled(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": false}`)
	withTempCacheDir(t)

	s := &RSSService{}
	content := `<p>hello</p><img src="http://example.com/pic.png">`

	got := s.sanitizeContent(context.Background(), 1, content)

	if !strings.Contains(got, "http://example.com/pic.png") {
		t.Errorf("关闭缓存后应保留原始外链，实际: %s", got)
	}
}

// TestSanitizeContentCachesImages 确认开启时把外链改写为站内路径。
func TestSanitizeContentCachesImages(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": true, "image_cache_concurrency": 4, "image_allow_private_hosts": true}`)
	dir := withTempCacheDir(t)

	// 假图片服务器
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	s := &RSSService{}
	content := fmt.Sprintf(`<img src="%s/a.png">`, srv.URL)

	got := s.sanitizeContent(context.Background(), 1, content)

	if strings.Contains(got, srv.URL) {
		t.Errorf("应改写为站内 URL，实际: %s", got)
	}
	if !strings.Contains(got, "/static/uploads/rss_img/rss_") {
		t.Errorf("缺少站内图片路径，实际: %s", got)
	}
	if hits.Load() == 0 {
		t.Error("没有发起图片下载请求")
	}

	// 文件确实落盘
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取缓存目录失败: %v", err)
	}
	if len(entries) == 0 {
		t.Error("缓存目录为空，图片未落盘")
	}
}

// TestSanitizeContentDownloadsConcurrently 确认多张图片并发下载。
//
// 串行实现下 4 张各 100ms 的图片需要 400ms；并发（>=2）应明显更快。
// 这里用较宽松的阈值避免 CI 抖动导致误报。
func TestSanitizeContentDownloadsConcurrently(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": true, "image_cache_concurrency": 4, "image_allow_private_hosts": true}`)
	withTempCacheDir(t)

	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		inFlight.Add(-1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	var b strings.Builder
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, `<img src="%s/%d.png">`, srv.URL, i)
	}

	s := &RSSService{}
	start := time.Now()
	s.sanitizeContent(context.Background(), 1, b.String())
	elapsed := time.Since(start)

	if maxInFlight.Load() < 2 {
		t.Errorf("最大并发数 = %d，期望 >= 2（实现仍是串行？）", maxInFlight.Load())
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("4 张图耗时 %v，接近串行的 320ms，并发未生效", elapsed)
	}
}

// TestSanitizeContentRespectsBudget 确认超时预算是硬约束。
//
// 图片服务器比预算还慢时，抓取不应被拖住：预算耗尽后放弃图片、
// 条目照常入库（图片标签被移除）。
func TestSanitizeContentRespectsBudget(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": true, "image_cache_concurrency": 4, "image_allow_private_hosts": true}`)
	withTempCacheDir(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte("slow"))
	}))
	defer srv.Close()

	s := &RSSService{}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := s.sanitizeContent(ctx, 1, fmt.Sprintf(`<p>keep</p><img src="%s/a.png">`, srv.URL))
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("预算 150ms，实际耗时 %v", elapsed)
	}
	if !strings.Contains(got, "keep") {
		t.Error("文本内容不应被丢弃")
	}
	if strings.Contains(got, srv.URL) {
		t.Error("超时的图片应被移除，而不是保留外链")
	}
}

// TestRSSFetcherCanBeReplaced 确认 SetFetcher 允许替换实现（extension 接入点）。
func TestRSSFetcherCanBeReplaced(t *testing.T) {
	fake := &fakeFetcher{}
	s := NewRSSService(nil)
	s.SetFetcher(fake)

	feed, err := s.fetcher.Fetch(context.Background(), "http://example.com/feed")
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if feed.Title != "test" {
		t.Errorf("未使用替换后的 fetcher，title = %q", feed.Title)
	}
	if fake.calls.Load() != 1 {
		t.Errorf("fake fetcher 调用次数 = %d，期望 1", fake.calls.Load())
	}

	// 传 nil 应恢复为按配置自动选择
	s.SetFetcher(nil)
	if _, ok := s.fetcher.(*autoFetcher); !ok {
		t.Errorf("SetFetcher(nil) 应恢复 autoFetcher，实际 %T", s.fetcher)
	}
}

// TestSanitizeContentKeepsRelativeImages 确认相对路径/data URL 不会被误删。
//
// 只有真正尝试缓存但失败的图片才应移除；/<path> 与 data: 不参与缓存，
// 必须原样保留。
func TestSanitizeContentKeepsRelativeImages(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": true, "image_allow_private_hosts": true}`)
	withTempCacheDir(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	s := &RSSService{}
	content := fmt.Sprintf(
		`<p>keep</p><img src="/static/uploads/local.png"><img src="data:image/png;base64,AAAA"><img src="%s/missing.png">`,
		srv.URL,
	)

	got := s.sanitizeContent(context.Background(), 1, content)

	if !strings.Contains(got, "/static/uploads/local.png") {
		t.Errorf("相对路径图片应保留，实际: %s", got)
	}
	if !strings.Contains(got, "data:image/png;base64,AAAA") {
		t.Errorf("data URL 应保留，实际: %s", got)
	}
	if strings.Contains(got, srv.URL) {
		t.Errorf("下载失败的外链图片应移除，实际: %s", got)
	}
}

// blockingFetcher 在 release 关闭前一直阻塞，用来制造「正在抓取」的窗口。
type blockingFetcher struct {
	entered chan struct{}
	release chan struct{}
}

func (f *blockingFetcher) Fetch(ctx context.Context, url string) (*gofeed.Feed, error) {
	close(f.entered)
	<-f.release
	return nil, fmt.Errorf("测试结束")
}

// TestFetchFeedRejectsConcurrentFetchOfSameFeed 防的是同一个源被同时抓两次。
//
// 后台调度、手动「抓取」按钮、FetchAll 是三条独立入口。同一个源并发
// 抓取会让两条 upsert 争同一批行（死锁或重复图片下载），而结果只会
// 有一份。第二个调用必须立刻得到明确的忙碌错误，而不是排队再抓一遍。
func TestFetchFeedRejectsConcurrentFetchOfSameFeed(t *testing.T) {
	db, mock, cleanup := testutil.NewUnorderedMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, now, now, nil, "源", "https://a.example/f", "", "", "", "",
				3600, model.FetchByLocal, true, now, "", nil))
	mock.ExpectExec(`UPDATE "rss_feeds"`).WillReturnResult(sqlmock.NewResult(0, 1))

	f := &blockingFetcher{entered: make(chan struct{}), release: make(chan struct{})}
	svc := &RSSService{db: db, fetcher: f}

	firstDone := make(chan error, 1)
	go func() { firstDone <- svc.FetchFeed(context.Background(), 1) }()
	<-f.entered

	if err := svc.FetchFeed(context.Background(), 1); !errors.Is(err, ErrRSSFeedBusy) {
		t.Fatalf("同一源正在抓取时第二次调用应返回 ErrRSSFeedBusy，got %v", err)
	}

	close(f.release)
	<-firstDone

	// 第一次结束后必须释放占用，否则这个源永远抓不了。
	if svc.tryBeginFetch(1) == false {
		t.Fatal("抓取结束后应释放占用")
	}
	svc.endFetch(1)
}

// TestFetchAllSkipsBusyFeedWithoutReportingFailure 确认 FetchAll 把忙碌当成跳过。
//
// 手动抓取正在进行时后台恰好到期：这不是失败，不该让本轮被记成
// 「部分失败」并刷一条警告。
func TestFetchAllSkipsBusyFeedWithoutReportingFailure(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "rss_feeds"`).
		WillReturnRows(sqlmock.NewRows(scheduleFeedColumns()).
			AddRow(1, now, now, nil, "源", "https://a.example/f", "", "", "", "",
				3600, model.FetchByLocal, true, now.Add(-2*time.Hour), "", nil))

	svc := &RSSService{db: db, fetcher: &fakeFetcher{}}
	if !svc.tryBeginFetch(1) {
		t.Fatal("占用失败")
	}
	defer svc.endFetch(1)

	if err := svc.FetchAll(context.Background()); err != nil {
		t.Fatalf("忙碌的源应被静默跳过，got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("不应为忙碌的源发出任何查询: %v", err)
	}
}
