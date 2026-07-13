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
// 使用 ILIKE '%keyword%' 进行模糊匹配。为获得索引加速，需预先执行
// migrations/001_search_trgm_indexes.sql 创建 pg_trgm GIN 索引。
// 不使用 pg_trgm 时，全表扫描在大数据量下性能急剧下降。
type SearchService struct {
	db *gorm.DB
}

// SearchResult 搜索结果。
type SearchResult struct {
	Bookmarks []BookmarkSearchItem `json:"bookmarks"`
}

// BookmarkSearchItem 书签搜索结果。
type BookmarkSearchItem struct {
	ID        uint64 `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	GroupName string `json:"group_name"`
}

// VideoSearchItem 视频搜索结果。
// ArticleSearchItem 专栏搜索结果。
// UserSearchItem 用户搜索结果。
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

// Search 执行跨源搜索，scope 可选：all/bookmarks/videos/articles/users。
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
			FROM bookmarks b
			JOIN bookmark_groups bg ON bg.id = b.group_id
			WHERE b.deleted_at IS NULL AND bg.deleted_at IS NULL
			  AND (b.title ILIKE ? OR b.url ILIKE ?)
			ORDER BY b.priority DESC, b.sort_order ASC
			LIMIT ?
		`, pattern, pattern, searchLimitPerSource).Scan(&items)
		mu.Lock()
		result.Bookmarks = items
		mu.Unlock()
	}

	dispatch := map[string]func(){
		"all":       func() { wg.Add(1); safeGo("bookmarks", searchBookmarks) },
		"bookmarks": func() { wg.Add(1); safeGo("bookmarks", searchBookmarks) },
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
