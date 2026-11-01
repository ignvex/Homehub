package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// ============================================================
// 跨源搜索
// ============================================================

// SearchService 跨数据源搜索服务。
//
// 匹配用 ILIKE '%keyword%'，pg_trgm GIN 索引由
// database.EnsureSearchIndexes 在启动时自动创建；扩展不可用时
// 退化为全表扫描，功能不受影响。
//
// 排序**不**依赖 similarity()：那是 pg_trgm 的函数，扩展没装时整条
// 查询会报错。这里用 CASE 权重（标题命中 < 分组/作者命中 < 正文命中），
// 在任何 PostgreSQL 上都能跑，且更贴近使用者的直觉——搜一个词，
// 标题里就叫这个词的条目显然比正文里提到一次的更相关。
type SearchService struct {
	db *gorm.DB
}

// SearchOptions 是一次搜索的参数。
type SearchOptions struct {
	Query string
	// Scope 取 all / bookmarks / rss。
	Scope string
	// Page 从 1 开始；PerPage 默认 DefaultSearchPerPage。
	Page    int
	PerPage int
	// FeedID / GroupID 为 0 表示不过滤。
	FeedID  uint64
	GroupID uint64
}

// 分页默认值与上限。
//
// 上限存在的意义不是省内存，而是防止一次请求把整库拉进内存导致 SQL
// 超时——那种失败在前端看起来只是「搜索很慢」，很难归因。
const (
	DefaultSearchPerPage = 20
	MaxSearchPerPage     = 100
)

// SearchResult 搜索结果。
type SearchResult struct {
	Query string `json:"query"`
	// Scope 原样回传，让前端知道这次返回覆盖了哪些源。
	Scope string `json:"scope"`

	Bookmarks      []BookmarkSearchItem `json:"bookmarks"`
	BookmarksTotal int                  `json:"bookmarks_total"`

	RssEntries      []RssEntrySearchItem `json:"rss_entries"`
	RssEntriesTotal int                  `json:"rss_entries_total"`

	// Page / PerPage 回传生效值：前端传了超限的 per_page 时，
	// 它需要知道实际用了多少才能正确算页数。
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	// TookMs 是服务端耗时，便于判断慢查询。
	TookMs int64 `json:"took_ms"`
}

// RssEntrySearchItem RSS 条目搜索结果。
type RssEntrySearchItem struct {
	ID        uint64   `json:"id"`
	Title     string   `json:"title"`
	Link      string   `json:"link"`
	Author    string   `json:"author,omitempty"`
	FeedTitle string   `json:"feed_title"`
	FeedIDs   []uint64 `json:"feed_ids,omitempty"`
	// Snippet 是正文里命中关键词附近的一小段纯文本，供前端高亮。
	// 为空表示命中在标题或作者上，正文摘要没有意义。
	Snippet string `json:"snippet,omitempty"`
}

// BookmarkSearchItem 书签搜索结果。
type BookmarkSearchItem struct {
	ID        uint64 `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	GroupName string `json:"group_name"`
}

// 搜索的调用方错误。导出是为了让 handler 用 errors.Is 判定状态码，
// 而不是匹配错误文本——文案会改，状态码不该跟着改。
var (
	ErrSearchEmptyQuery = errors.New("请提供搜索关键词")
	ErrSearchBadScope   = errors.New("不支持的搜索范围")
)

// NewSearchService 创建 SearchService 实例。
func NewSearchService(db *gorm.DB) *SearchService {
	return &SearchService{db: db}
}

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

// normalizeSearchOptions 补齐默认值并夹紧分页。
func normalizeSearchOptions(opts SearchOptions) SearchOptions {
	opts.Query = strings.TrimSpace(opts.Query)
	if opts.Scope == "" {
		opts.Scope = "all"
	}
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PerPage <= 0 {
		opts.PerPage = DefaultSearchPerPage
	}
	if opts.PerPage > MaxSearchPerPage {
		opts.PerPage = MaxSearchPerPage
	}
	return opts
}

// likePattern 转义 LIKE 的通配符。
//
// 不转义的话，搜 "100%" 会变成「以 100 开头的任意内容」，搜 "_"
// 会匹配任意单字符——用户输入的字面量必须被当成字面量。
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return "%" + r.Replace(q) + "%"
}

// Search 执行跨源搜索。
func (s *SearchService) Search(opts SearchOptions) (*SearchResult, error) {
	opts = normalizeSearchOptions(opts)
	if opts.Query == "" {
		return nil, ErrSearchEmptyQuery
	}

	started := time.Now()
	pattern := likePattern(opts.Query)
	offset := (opts.Page - 1) * opts.PerPage

	result := &SearchResult{
		Query:   opts.Query,
		Scope:   opts.Scope,
		Page:    opts.Page,
		PerPage: opts.PerPage,
		// 初始化成空切片而不是 nil：前端可以无条件 .map，
		// JSON 里也不会出现 null。
		Bookmarks:  []BookmarkSearchItem{},
		RssEntries: []RssEntrySearchItem{},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	recordErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	searchBookmarks := func() {
		defer wg.Done()
		items, total, err := s.searchBookmarks(pattern, opts, offset)
		if err != nil {
			recordErr(err)
			return
		}
		mu.Lock()
		result.Bookmarks, result.BookmarksTotal = items, total
		mu.Unlock()
	}

	searchRssEntries := func() {
		defer wg.Done()
		items, total, err := s.searchRssEntries(pattern, opts, offset)
		if err != nil {
			recordErr(err)
			return
		}
		mu.Lock()
		result.RssEntries, result.RssEntriesTotal = items, total
		mu.Unlock()
	}

	switch opts.Scope {
	case "all":
		wg.Add(2)
		safeGo("bookmarks", searchBookmarks)
		safeGo("rss", searchRssEntries)
	case "bookmarks":
		wg.Add(1)
		safeGo("bookmarks", searchBookmarks)
	case "rss":
		wg.Add(1)
		safeGo("rss", searchRssEntries)
	default:
		return nil, fmt.Errorf("%w: %s", ErrSearchBadScope, opts.Scope)
	}

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

	if firstErr != nil {
		return nil, firstErr
	}

	result.TookMs = time.Since(started).Milliseconds()
	return result, nil
}

// searchBookmarks 查书签，返回一页结果与总数。
func (s *SearchService) searchBookmarks(pattern string, opts SearchOptions, offset int) ([]BookmarkSearchItem, int, error) {
	where := `b.deleted_at IS NULL AND bg.deleted_at IS NULL
		AND (b.title ILIKE ? OR b.url ILIKE ? OR bg.name ILIKE ?)`
	args := []any{pattern, pattern, pattern}
	if opts.GroupID > 0 {
		where += " AND b.group_id = ?"
		args = append(args, opts.GroupID)
	}

	var total int
	if err := s.db.Raw(`
		SELECT count(*)
		FROM bookmarks b
		JOIN bookmark_groups bg ON bg.id = b.group_id
		WHERE `+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计书签失败: %w", err)
	}

	items := []BookmarkSearchItem{}
	if total == 0 {
		return items, 0, nil
	}

	// 排序权重：标题命中 > 分组名命中 > 仅 URL 命中。
	// 同一档内沿用用户自己的排序（priority、sort_order）——
	// 搜索结果不该打乱他手工排好的顺序。
	queryArgs := append(append([]any{}, args...), pattern, pattern, opts.PerPage, offset)
	if err := s.db.Raw(`
		SELECT b.id, b.title, b.url, bg.name AS group_name
		FROM bookmarks b
		JOIN bookmark_groups bg ON bg.id = b.group_id
		WHERE `+where+`
		ORDER BY
			CASE WHEN b.title ILIKE ? THEN 0
			     WHEN bg.name ILIKE ? THEN 1
			     ELSE 2 END,
			b.priority DESC, b.sort_order ASC, b.id ASC
		LIMIT ? OFFSET ?`, queryArgs...).Scan(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("查询书签失败: %w", err)
	}
	return items, total, nil
}

// searchRssEntries 查 RSS 条目，返回一页结果与总数。
func (s *SearchService) searchRssEntries(pattern string, opts SearchOptions, offset int) ([]RssEntrySearchItem, int, error) {
	where := `re.deleted_at IS NULL
		AND (re.title ILIKE ? OR re.content ILIKE ? OR re.author ILIKE ?)`
	args := []any{pattern, pattern, pattern}

	if opts.FeedID > 0 {
		// 条目跨源共享：一条文章可能被多个源收录。按源筛选要用 EXISTS，
		// 不能 JOIN 后过滤——那会把同一条目按源的数量重复输出。
		where += ` AND EXISTS (
			SELECT 1 FROM rss_entry_feeds ef
			WHERE ef.entry_id = re.id AND ef.feed_id = ? AND ef.deleted_at IS NULL)`
		args = append(args, opts.FeedID)
	}

	var total int
	if err := s.db.Raw(`SELECT count(*) FROM rss_entries re WHERE `+where, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计条目失败: %w", err)
	}

	items := []RssEntrySearchItem{}
	if total == 0 {
		return items, 0, nil
	}

	queryArgs := append(append([]any{}, args...), pattern, pattern, opts.PerPage, offset)
	var rows []rssEntrySearchRow
	if err := s.db.Raw(`
		SELECT re.id, re.title, re.link, re.author,
		       left(re.content, 600) AS content_head
		FROM rss_entries re
		WHERE `+where+`
		ORDER BY
			CASE WHEN re.title ILIKE ? THEN 0
			     WHEN re.author ILIKE ? THEN 1
			     ELSE 2 END,
			re.published DESC, re.id DESC
		LIMIT ? OFFSET ?`, queryArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询条目失败: %w", err)
	}

	// 只取前 600 个字符再在应用层去标签：SQL 里做 regexp_replace 对
	// 长正文是整表级开销，而摘要只需要开头一小段。
	for _, row := range rows {
		items = append(items, RssEntrySearchItem{
			ID:      row.ID,
			Title:   row.Title,
			Link:    row.Link,
			Author:  row.Author,
			Snippet: buildSnippet(row.ContentHead, opts.Query),
		})
	}

	// 补源标题与源 id。分页后只有 per_page 条，批量取源信息
	// 比逐行关联子查询便宜得多。
	if err := s.fillFeedInfo(items); err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// rssEntrySearchRow 是搜索中间行，避免把 content 直接暴露到 API 结构上。
type rssEntrySearchRow struct {
	ID          uint64 `gorm:"column:id"`
	Title       string `gorm:"column:title"`
	Link        string `gorm:"column:link"`
	Author      string `gorm:"column:author"`
	ContentHead string `gorm:"column:content_head"`
}

// feedLink 是条目与源的关联中间行。
type feedLink struct {
	EntryID   uint64 `gorm:"column:entry_id"`
	FeedID    uint64 `gorm:"column:feed_id"`
	FeedTitle string `gorm:"column:feed_title"`
}

// fillFeedInfo 批量补齐每条结果的源标题与源 id。
func (s *SearchService) fillFeedInfo(items []RssEntrySearchItem) error {
	if len(items) == 0 {
		return nil
	}

	ids := make([]uint64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	var links []feedLink
	if err := s.db.Raw(`
		SELECT ef.entry_id, f.id AS feed_id, f.title AS feed_title
		FROM rss_entry_feeds ef
		JOIN rss_feeds f ON f.id = ef.feed_id AND f.deleted_at IS NULL
		WHERE ef.entry_id IN ? AND ef.deleted_at IS NULL
		ORDER BY ef.id ASC`, ids).Scan(&links).Error; err != nil {
		return fmt.Errorf("查询条目来源失败: %w", err)
	}

	byEntry := make(map[uint64][]feedLink, len(items))
	for _, link := range links {
		byEntry[link.EntryID] = append(byEntry[link.EntryID], link)
	}

	for i := range items {
		feeds := byEntry[items[i].ID]
		if len(feeds) == 0 {
			continue
		}
		feedIDs := make([]uint64, 0, len(feeds))
		for _, f := range feeds {
			feedIDs = append(feedIDs, f.FeedID)
		}
		items[i].FeedIDs = feedIDs
		items[i].FeedTitle = feeds[0].FeedTitle
	}
	return nil
}

// htmlTagRe 匹配 HTML 标签，用于从正文里抽纯文本摘要。
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// snippetRadius 是摘要里关键词左右保留的字符数。
const snippetRadius = 60

// buildSnippet 从正文片段里抽出一小段纯文本，尽量包含关键词。
//
// 找不到关键词时返回空串而不是开头一段：前端据此区分「命中在正文」
// 与「命中在标题」。给一段无关的正文开头反而会让人以为搜错了。
func buildSnippet(content, query string) string {
	if content == "" || query == "" {
		return ""
	}

	text := htmlTagRe.ReplaceAllString(content, " ")
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}

	idx := indexFold(text, query)
	if idx < 0 {
		return ""
	}

	start := idx - snippetRadius
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + snippetRadius
	if end > len(text) {
		end = len(text)
	}

	// 按字节切会切碎多字节字符（PostgreSQL 会直接拒收整批写入，
	// 前端则会渲染出替换字符）。这里退到最近的字符边界。
	start = alignRuneStart(text, start)
	end = alignRuneEnd(text, end)

	out := strings.TrimSpace(text[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

// indexFold 做大小写无关的字节级查找。
func indexFold(haystack, needle string) int {
	return strings.Index(strings.ToLower(haystack), strings.ToLower(needle))
}

// alignRuneStart 把索引退到 UTF-8 字符起点。
func alignRuneStart(s string, i int) int {
	for i > 0 && i < len(s) && (s[i]&0xC0) == 0x80 {
		i--
	}
	return i
}

// alignRuneEnd 把索引推进到 UTF-8 字符边界。
func alignRuneEnd(s string, i int) int {
	for i < len(s) && (s[i]&0xC0) == 0x80 {
		i++
	}
	return i
}
