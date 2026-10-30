package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
)

// SearchService 跨数据源搜索服务。
//
// 使用 ILIKE '%keyword%' 进行模糊匹配。pg_trgm GIN 索引由
// database.EnsureSearchIndexes 在启动时自动创建；扩展不可用时
// 退化为全表扫描，数据量大时性能会明显下降。
type SearchService struct {
	db *gorm.DB
}

// SearchResult 搜索结果。
type SearchResult struct {
	Bookmarks  []BookmarkSearchItem  `json:"bookmarks"`
	RssEntries []RssEntrySearchItem `json:"rss_entries"`
}

// RssEntrySearchItem RSS 条目搜索结果。
type RssEntrySearchItem struct {
	ID       uint64 `json:"id"`
	Title    string `json:"title"`
	Link     string `json:"link"`
	FeedTitle string `json:"feed_title"`
}

// BookmarkSearchItem 书签搜索结果。
type BookmarkSearchItem struct {
	ID        uint64 `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	GroupName string `json:"group_name"`
}

// NewSearchService 创建 SearchService 实例。
func NewSearchService(db *gorm.DB) *SearchService {
	return &SearchService{db: db}
}

const searchLimitPerSource = 10

// safeGo 带 panic recovery 启动 goroutine。
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[search] goroutine %s panic: %v", name, r)
			}
		}()
		fn()
	}()
}

// Search 执行跨源搜索，scope 可选：all/bookmarks/rss。
func (s *SearchService) Search(q string, scope string) (*SearchResult, error) {
	result := &SearchResult{}
	pattern := "%" + q + "%"

	var wg sync.WaitGroup
	var mu sync.Mutex

	searchBookmarks := func() {
		defer wg.Done()
		var items []BookmarkSearchItem
		s.db.Raw(`
			SELECT b.id, b.title, b.url, bg.name AS group_name
			FROM homehub_bookmarks b
			JOIN homehub_bookmark_groups bg ON bg.id = b.group_id
			WHERE b.deleted_at IS NULL AND bg.deleted_at IS NULL
			  AND (b.title ILIKE ? OR b.url ILIKE ?)
			ORDER BY b.priority DESC, b.sort_order ASC
			LIMIT ?
		`, pattern, pattern, searchLimitPerSource).Scan(&items)
		mu.Lock()
		result.Bookmarks = items
		mu.Unlock()
	}

	searchRssEntries := func() {
		defer wg.Done()
		var items []RssEntrySearchItem
		// 条目跨源共享：按 entry_id 去重，feed_title 取首个收录它的源。
		s.db.Raw(`
			SELECT re.id, re.title, re.link,
			       (SELECT f.title
			        FROM homehub_rss_entry_feeds ef
			        JOIN homehub_rss_feeds f ON f.id = ef.feed_id AND f.deleted_at IS NULL
			        WHERE ef.entry_id = re.id AND ef.deleted_at IS NULL
			        ORDER BY ef.id ASC
			        LIMIT 1) AS feed_title
			FROM homehub_rss_entries re
			WHERE re.deleted_at IS NULL
			  AND re.title ILIKE ?
			ORDER BY re.published DESC
			LIMIT ?
		`, pattern, searchLimitPerSource).Scan(&items)
		mu.Lock()
		result.RssEntries = items
		mu.Unlock()
	}

	dispatch := map[string]func(){
		"all":       func() { wg.Add(2); safeGo("bookmarks", searchBookmarks); safeGo("rss", searchRssEntries) },
		"bookmarks": func() { wg.Add(1); safeGo("bookmarks", searchBookmarks) },
		"rss":       func() { wg.Add(1); safeGo("rss", searchRssEntries) },
	}

	fn, ok := dispatch[scope]
	if !ok {
		return nil, fmt.Errorf("不支持的搜索范围: %s", scope)
	}
	fn()

	// 带超时的等待，防止 goroutine 永久阻塞
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	select {
	case <-done:
	case <-ctx.Done():
		return nil, fmt.Errorf("搜索超时")
	}

	return result, nil
}
