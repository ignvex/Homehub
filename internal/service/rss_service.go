package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/mmcdole/gofeed"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RSS 订阅相关业务错误。
var (
	ErrRSSFeedNotFound = errors.New("订阅源不存在")
	ErrRSSFeedExists   = errors.New("订阅源已存在")
)

// RSSService RSS 订阅业务服务。
//
// 负责：
//   - RSS 订阅源的增删改查
//   - RSS 条目的分页查询
//   - 定时抓取 RSS 内容并入库（支持代理）
//   - 条目保留数量控制
type RSSService struct {
	db      *gorm.DB
	fetcher RSSFetcher

	// imagesMu 保护 images：抓取是并发的，SetImageCache 可能在运行期
	// 被 extension 集成方或测试调用。
	imagesMu sync.RWMutex
	images   RSSImageCache
}

// NewRSSService 创建 RSSService 实例。
//
// 默认抓取器跟随配置：rss.extension_url 为空时本地抓取，否则交给
// extension 服务（HomeHub 单向发起请求）。要完全接管，可调用 SetFetcher。
func NewRSSService(db *gorm.DB) *RSSService {
	return &RSSService{
		db:      db,
		fetcher: newAutoFetcher(),
		images:  NewLocalImageCache(db),
	}
}

// SetImageCache 替换图片缓存实现。
//
// 这是为 homehub-extension 远程预下载预留的接入点：extension 集成方
// 可以实现 RSSImageCache，或在预下载后复用 LocalImageCache 的登记能力。
// 传入 nil 时恢复本地下载实现。
func (s *RSSService) SetImageCache(c RSSImageCache) {
	if c == nil {
		c = NewLocalImageCache(s.db)
	}
	s.imagesMu.Lock()
	s.images = c
	s.imagesMu.Unlock()
}

// imageCache 返回当前图片缓存实现；零值 RSSService（测试中常见）
// 没有注入实现时回落为本地下载。
func (s *RSSService) imageCache() RSSImageCache {
	s.imagesMu.RLock()
	c := s.images
	s.imagesMu.RUnlock()
	if c != nil {
		return c
	}
	return NewLocalImageCache(s.db)
}

// SetFetcher 强制指定抓取实现，覆盖按配置的自动选择。
//
// 传入 nil 时恢复为自动选择（本地 / extension 跟随配置）。
func (s *RSSService) SetFetcher(f RSSFetcher) {
	if f == nil {
		f = newAutoFetcher()
	}
	s.fetcher = f
}

// ============================================================
// 订阅源 CRUD
// ============================================================

// CreateFeed 创建新的 RSS 订阅源。
//
// 先检查 URL 是否已存在，然后通过 gofeed 解析订阅源获取元数据，
// 最后创建 RSSFeed 记录并返回。
func (s *RSSService) CreateFeed(urlStr string, groupID *uint64) (*model.RSSFeed, error) {
	if urlStr == "" {
		return nil, errors.New("订阅源 URL 不能为空")
	}

	// SSRF 防护：校验 URL 合法性
	if err := validateFeedURL(urlStr); err != nil {
		return nil, err
	}

	// 检查是否已存在
	var count int64
	s.db.Model(&model.RSSFeed{}).Where("url = ?", urlStr).Count(&count)
	if count > 0 {
		return nil, ErrRSSFeedExists
	}

	// 解析订阅源获取元数据（此时尚未入库，用请求级 ctx 控制超时）
	feed, err := s.fetcher.Fetch(context.Background(), urlStr)
	if err != nil {
		return nil, fmt.Errorf("解析订阅源失败: %w", err)
	}

	// 构造 RSSFeed 记录
	rssFeed := model.RSSFeed{
		URL:           urlStr,
		Title:         feed.Title,
		SiteURL:       feed.Link,
		Description:   feed.Description,
		FetchInterval: 3600, // 默认 1 小时
	}

	if groupID != nil {
		rssFeed.GroupID = groupID
	}

	// 提取站点图标
	if feed.Image != nil && feed.Image.URL != "" {
		rssFeed.IconURL = feed.Image.URL
	}

	if err := s.db.Create(&rssFeed).Error; err != nil {
		return nil, fmt.Errorf("创建订阅源失败: %w", err)
	}

	// 立即执行首次抓取，填充初始条目。
	// 用独立 ctx：请求返回后仍要继续，但需有超时避免永久挂起。
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rssFetchBudget())
		defer cancel()
		if err := s.FetchFeed(ctx, rssFeed.ID); err != nil {
			logger.Error("[rss] 首次抓取失败", "feed_id", rssFeed.ID, "error", err)
		}
	}()

	return &rssFeed, nil
}

// UpdateFeed 更新订阅源标题、抓取间隔和所属分组。
func (s *RSSService) UpdateFeed(id uint64, title string, fetchInterval int, groupID *uint64) error {
	updates := map[string]interface{}{}
	if title != "" {
		updates["title"] = title
	}
	if fetchInterval > 0 {
		updates["fetch_interval"] = fetchInterval
	}
	if groupID != nil {
		updates["group_id"] = *groupID
	}
	if len(updates) == 0 {
		return nil
	}

	result := s.db.Model(&model.RSSFeed{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("更新订阅源失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrRSSFeedNotFound
	}
	return nil
}

// DeleteFeed 删除订阅源及其所有条目，并回收不再被其他源引用的图片。
func (s *RSSService) DeleteFeed(id uint64) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 条目/图片引用都做物理删除：软删除会让表无限膨胀，
		// 而 retention 语义本来就是“真的不要了”。
		if err := tx.Unscoped().Where("feed_id = ?", id).Delete(&model.RSSEntry{}).Error; err != nil {
			return fmt.Errorf("删除订阅条目失败: %w", err)
		}
		if err := tx.Unscoped().Where("feed_id = ?", id).Delete(&model.RSSImageRef{}).Error; err != nil {
			return fmt.Errorf("删除图片引用失败: %w", err)
		}
		// 再删订阅源
		result := tx.Unscoped().Delete(&model.RSSFeed{}, id)
		if result.Error != nil {
			return fmt.Errorf("删除订阅源失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrRSSFeedNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 立即清理不再被任何条目引用的图片文件。
	s.sweepImages(context.Background(), 0)
	return nil
}

// GetFeed 根据 ID 获取订阅源。
func (s *RSSService) GetFeed(id uint64) (*model.RSSFeed, error) {
	var feed model.RSSFeed
	if err := s.db.First(&feed, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRSSFeedNotFound
		}
		return nil, fmt.Errorf("查询订阅源失败: %w", err)
	}
	return &feed, nil
}

// ListFeeds 返回所有订阅源列表，可按分组筛选。
func (s *RSSService) ListFeeds(groupID *uint64) ([]model.RSSFeed, error) {
	var feeds []model.RSSFeed
	query := s.db.Order("created_at DESC")
	if groupID != nil {
		query = query.Where("group_id = ?", *groupID)
	}
	if err := query.Find(&feeds).Error; err != nil {
		return nil, fmt.Errorf("查询订阅源列表失败: %w", err)
	}
	return feeds, nil
}

// ============================================================
// 条目查询
// ============================================================

// ListEntries 分页查询指定订阅源的条目，按发布时间倒序。
//
// page 从 1 开始；perPage <= 0 时默认 20。
// feedID == 0 表示不过滤，查询全部订阅源的条目。
func (s *RSSService) ListEntries(feedID uint64, groupID uint64, page, perPage int) ([]model.RSSEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 20
	}

	baseQuery := s.db.Model(&model.RSSEntry{})
	if feedID > 0 {
		baseQuery = baseQuery.Where("feed_id = ?", feedID)
	}
	if groupID > 0 {
		baseQuery = baseQuery.
			Joins("JOIN homehub_rss_feeds ON homehub_rss_feeds.id = homehub_rss_entries.feed_id").
			Where("homehub_rss_feeds.group_id = ?", groupID)
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计条目数量失败: %w", err)
	}

	var entries []model.RSSEntry
	offset := (page - 1) * perPage
	if err := baseQuery.
		Order("published DESC").
		Offset(offset).
		Limit(perPage).
		Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("查询条目列表失败: %w", err)
	}

	return entries, total, nil
}

// ============================================================
// 抓取逻辑
// ============================================================

// FetchFeed 抓取指定订阅源的最新内容。
//
// 流程：
//  1. 获取订阅源记录
//  2. 通过 HTTP 代理抓取 RSS XML
//  3. 用 gofeed 解析
//  4. 插入条目（ON CONFLICT ON link DO NOTHING）
//  5. 按 retention_count 清理超量旧条目
//  6. 更新 LastFetch 和 ErrorMsg
func (s *RSSService) FetchFeed(ctx context.Context, id uint64) error {
	var feed model.RSSFeed
	if err := s.db.First(&feed, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRSSFeedNotFound
		}
		return fmt.Errorf("查询订阅源失败: %w", err)
	}

	// 每次抓取前复验 URL（防止 DNS rebinding 绕过创建时的校验）
	if err := validateFeedURL(feed.URL); err != nil {
		return fmt.Errorf("订阅源 URL 校验失败: %w", err)
	}

	// 抓取并解析（超时由调用方的 ctx 控制）
	parsed, err := s.fetcher.Fetch(ctx, feed.URL)
	if err != nil {
		// 更新错误信息
		errMsg := err.Error()
		if len(errMsg) > 1024 {
			errMsg = errMsg[:1024]
		}
		s.db.Model(&feed).Updates(map[string]interface{}{
			"last_fetch": time.Now(),
			"error_msg":  errMsg,
		})
		return fmt.Errorf("抓取订阅源失败: %w", err)
	}

	// 收集条目
	now := time.Now()
	entries := make([]model.RSSEntry, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		entry := s.itemToEntry(ctx, feed.ID, item, now)
		if entry.Link == "" {
			continue // 跳过无链接条目
		}
		entries = append(entries, entry)
	}

	// 批量插入（ON CONFLICT DO UPDATE magnet_url）
	if len(entries) > 0 {
		if err := s.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "link"}},
			DoUpdates: clause.AssignmentColumns([]string{"magnet_url"}),
		}).Create(&entries).Error; err != nil {
			errMsg := err.Error()
			if len(errMsg) > 1024 {
				errMsg = errMsg[:1024]
			}
			s.db.Model(&feed).Updates(map[string]interface{}{
				"last_fetch": now,
				"error_msg":  errMsg,
			})
			return fmt.Errorf("保存条目失败: %w", err)
		}
	}

	// 诊断：记录成功抓取的条目数
	logger.Info("[rss] 抓取完成", "feed_id", feed.ID, "entries", len(entries))

	// 执行保留策略
	if err := s.enforceRetention(ctx, feed.ID); err != nil {
		errMsg := err.Error()
		if len(errMsg) > 1024 {
			errMsg = errMsg[:1024]
		}
		s.db.Model(&feed).Updates(map[string]interface{}{
			"last_fetch": now,
			"error_msg":  errMsg,
		})
		return err
	}

	// 更新成功状态
	updates := map[string]interface{}{
		"last_fetch": now,
		"error_msg":  "",
	}
	// 如果标题发生了变化，更新标题
	if parsed.Title != "" && parsed.Title != feed.Title {
		updates["title"] = parsed.Title
	}
	if parsed.Description != "" {
		updates["description"] = parsed.Description
	}
	if parsed.Link != "" {
		updates["site_url"] = parsed.Link
	}
	if parsed.Image != nil && parsed.Image.URL != "" {
		updates["icon_url"] = parsed.Image.URL
	}

	s.db.Model(&feed).Updates(updates)

	return nil
}

// rssConcurrency 返回订阅源抓取并发数（配置热更新即时生效）。
func rssConcurrency() int {
	cfg := config.Get()
	if cfg != nil && cfg.RSS.FetchConcurrency > 0 {
		return cfg.RSS.FetchConcurrency
	}
	return 4
}

// rssFetchBudget 返回单个订阅源的抓取预算（含图片缓存）。
func rssFetchBudget() time.Duration {
	cfg := config.Get()
	if cfg != nil && cfg.RSS.FetchTimeoutSeconds > 0 {
		return time.Duration(cfg.RSS.FetchTimeoutSeconds) * time.Second
	}
	return rssHTTPTimeout
}

// FetchAll 并发抓取所有到期的订阅源。
//
// 并发上限来自 rss.fetch_concurrency，每个订阅源有独立的超时预算：
// 单个源慢或挂掉只会占用一个并发槽，不会拖住整轮——这是之前串行实现
// 最大的问题（N 个源各超时 30s 会让后台循环彻底堵死）。
//
// 返回所有失败源的聚合错误；成功的源不受影响。
func (s *RSSService) FetchAll(ctx context.Context) error {
	var feeds []model.RSSFeed
	if err := s.db.Find(&feeds).Error; err != nil {
		return fmt.Errorf("查询订阅源列表失败: %w", err)
	}

	now := time.Now()
	due := make([]model.RSSFeed, 0, len(feeds))
	for _, feed := range feeds {
		interval := feed.FetchInterval
		if interval <= 0 {
			interval = 3600
		}
		if now.Sub(feed.LastFetch) <= time.Duration(interval)*time.Second {
			continue
		}
		due = append(due, feed)
	}

	if len(due) == 0 {
		return nil
	}

	budget := rssFetchBudget()
	sem := make(chan struct{}, rssConcurrency())
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, feed := range due {
		wg.Add(1)
		go func(feedID uint64) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			fctx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()

			if err := s.FetchFeed(fctx, feedID); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("feed %d: %w", feedID, err))
				mu.Unlock()
			}
		}(feed.ID)
	}

	wg.Wait()

	if len(errs) > 0 {
		logger.Warn("[rss] 本轮抓取部分失败", "failed", len(errs), "total", len(due))
		return errors.Join(errs...)
	}
	logger.Info("[rss] 本轮抓取完成", "feeds", len(due))
	return nil
}

// backgroundTick 是后台调度器的检查间隔。
//
// 用短 tick 轮询而不是"睡满一个 interval"，是为了让配置热更新
// 在一个 tick 内生效：否则改完抓取间隔要等整轮结束才起作用。
const backgroundTick = 5 * time.Second

// StartBackgroundFetcher 启动 RSS 后台定时抓取 goroutine。
//
// 每 backgroundTick 检查一次：
//   - 配置（开关/间隔）变化时重置计时，立即按新配置执行
//   - 到达下次抓取时间时执行一轮并发抓取
//
// ctx 取消时停止。
func (s *RSSService) StartBackgroundFetcher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(backgroundTick)
		defer ticker.Stop()

		var nextRun time.Time
		var lastPlan string

		for {
			cfg := config.Get()
			enabled := cfg != nil && cfg.RSS.BackgroundFetchEnabled
			interval := 300
			if cfg != nil && cfg.RSS.BackgroundFetchInterval > 0 {
				interval = cfg.RSS.BackgroundFetchInterval
			}
			if interval < 5 {
				interval = 5
			}

			plan := fmt.Sprintf("%v|%d", enabled, interval)
			if plan != lastPlan {
				lastPlan = plan
				// 配置变化：立刻按新配置跑一次，之后按新间隔调度。
				nextRun = time.Now()
				logger.Info("[rss] 后台抓取配置已更新", "enabled", enabled, "interval_seconds", interval)
			}

			if enabled && !time.Now().Before(nextRun) {
				if err := s.FetchAll(ctx); err != nil {
					logger.Warn("[rss] 后台抓取完成但有失败项", "error", err)
				}
				nextRun = time.Now().Add(time.Duration(interval) * time.Second)
			}

			select {
			case <-ctx.Done():
				logger.Info("[rss] 后台定时抓取已停止")
				return
			case <-ticker.C:
			}
		}
	}()
}

// StartImageJanitor 启动图片缓存清理任务。
//
// 启动后先跑一次（回收上次进程异常退出留下的临时文件/孤儿记录），
// 之后按 rss.image_cleanup_interval_hours 周期执行。配置热更新在下一轮
// 生效。无引用图片保留 rss.image_retention_days 天，避免刚下载、引用
// 尚未写入的图片被误删。
func (s *RSSService) StartImageJanitor(ctx context.Context) {
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()

		// 回填只需要成功执行一次；失败时下一轮重试。
		backfilled := false

		for {
			select {
			case <-ctx.Done():
				logger.Info("[rss] 图片缓存清理任务已停止")
				return
			case <-timer.C:
			}

			// 先回填升级前已有的缓存文件，再开始清理；顺序反了会把它们
			// 当成孤儿删掉，历史条目图片随即 404。
			// 回填没有成功就绝不能清理：否则历史文件会被当成孤儿删掉。
			ready := backfilled
			if !backfilled {
				if backfiller, ok := s.imageCache().(interface {
					BackfillLegacy(context.Context) error
				}); ok {
					if err := backfiller.BackfillLegacy(ctx); err != nil {
						if ctx.Err() == nil {
							logger.Warn("[rss] 回填历史图片失败，跳过本轮清理并在下一轮重试", "error", err)
						}
					} else {
						backfilled = true
						ready = true
					}
				} else {
					backfilled = true
					ready = true
				}
			}

			if ready {
				if cfg := config.Get(); cfg == nil || cfg.RSS.ImageCleanupEnabled {
					if err := s.imageCache().Sweep(ctx, imageRetentionGrace()); err != nil && ctx.Err() == nil {
						logger.Warn("[rss] 图片缓存清理失败", "error", err)
					}
				}
			}

			timer.Reset(imageCleanupInterval())
		}
	}()
}

// ============================================================
// 内部方法
// ============================================================

// itemToEntry 将 gofeed 条目转换为 RSSEntry 模型。
func (s *RSSService) itemToEntry(ctx context.Context, feedID uint64, item *gofeed.Item, fetchedAt time.Time) model.RSSEntry {
	entry := model.RSSEntry{
		FeedID:    feedID,
		Title:     item.Title,
		Link:      truncateStr(item.Link, 2048),
		FetchedAt: fetchedAt,
	}

	// 作者
	if item.Author != nil {
		entry.Author = item.Author.Name
	}

	// 内容：优先摘要，再取正文，截断到 2000 字符
	content := item.Description
	if content == "" {
		content = item.Content
	}
	if len(content) > 2000 {
		// 按 rune 截断
		runes := []rune(content)
		if len(runes) > 2000 {
			content = string(runes[:2000]) + "..."
		}
	}
	entry.Content = s.sanitizeContent(ctx, feedID, entry.Link, content)

	// 磁力链接：优先从 Enclosures 提取，fallback 正则扫描内容
	for _, enc := range item.Enclosures {
		if enc.URL != "" && strings.HasPrefix(enc.URL, "magnet:") {
			entry.MagnetURL = enc.URL
			break
		}
	}
	if entry.MagnetURL == "" && item.Description != "" {
		entry.MagnetURL = extractMagnet(item.Description)
	}
	if entry.MagnetURL == "" && item.Content != "" {
		entry.MagnetURL = extractMagnet(item.Content)
	}

	// 发布时间
	if item.PublishedParsed != nil {
		entry.Published = *item.PublishedParsed
	} else {
		entry.Published = fetchedAt
	}

	return entry
}

// enforceRetention 执行条目保留策略。
//
// 从配置读取 retention_count（默认 5000），超出限制时物理删除最旧的条目，
// 并同步删除这些条目的图片引用；随后回收无人使用的图片文件。
func (s *RSSService) enforceRetention(ctx context.Context, feedID uint64) error {
	retention := 5000
	cfg := config.Get()
	if cfg != nil && cfg.RSS.RetentionCount > 0 {
		retention = cfg.RSS.RetentionCount
	}

	var count int64
	if err := s.db.Model(&model.RSSEntry{}).Where("feed_id = ?", feedID).Count(&count).Error; err != nil {
		return fmt.Errorf("统计条目数量失败: %w", err)
	}

	if count <= int64(retention) {
		return nil
	}

	// 先取出要删除的条目（需要 link 来定位图片引用）。
	excess := int(count) - retention
	var stale []model.RSSEntry
	if err := s.db.Select("id", "link").
		Where("feed_id = ?", feedID).
		Order("published ASC, id ASC").
		Limit(excess).
		Find(&stale).Error; err != nil {
		return fmt.Errorf("查询待清理条目失败: %w", err)
	}
	if len(stale) == 0 {
		return nil
	}

	ids := make([]uint64, 0, len(stale))
	hashes := make([]string, 0, len(stale))
	for _, entry := range stale {
		ids = append(ids, entry.ID)
		if hash := sha256Hex(entry.Link); hash != "" {
			hashes = append(hashes, hash)
		}
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("id IN ?", ids).Delete(&model.RSSEntry{}).Error; err != nil {
			return fmt.Errorf("删除旧条目失败: %w", err)
		}
		if len(hashes) > 0 {
			if err := tx.Unscoped().
				Where("feed_id = ? AND entry_hash IN ?", feedID, hashes).
				Delete(&model.RSSImageRef{}).Error; err != nil {
				return fmt.Errorf("删除旧条目图片引用失败: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	s.sweepImages(ctx, 0)
	return nil
}

// sweepImages 调用当前图片缓存实现清理孤立文件；失败只告警。
func (s *RSSService) sweepImages(ctx context.Context, grace time.Duration) {
	if err := s.imageCache().Sweep(ctx, grace); err != nil {
		logger.Warn("[rss] 图片缓存清理失败", "error", err)
	}
}

// ============================================================
// URL 校验（SSRF 防护）
// ============================================================

// validateFeedURL 校验 RSS 订阅源 URL 的合法性，防止 SSRF 攻击。
//
// 规则：
//   - 仅允许 http/https 协议
//   - 禁止回环地址（127.0.0.0/8、::1）
//   - 禁止内网地址（10.0.0.0/8、172.16.0.0/12、192.168.0.0/16）
//   - 禁止云元数据端点（169.254.169.254）
func validateFeedURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("无效的 URL: %w", err)
	}

	// 仅允许 http/https
	scheme := parsed.Scheme
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("不支持的协议: %s（仅允许 http/https）", scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return errors.New("URL 中缺少主机名")
	}

	// 解析 IP
	ip := net.ParseIP(host)
	if ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("禁止访问内部地址: %s", host)
		}
		return nil
	}

	// 主机名黑名单
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".local") {
		return fmt.Errorf("禁止访问本地主机名: %s", host)
	}

	return nil
}

// ============================================================
// 分组 CRUD
// ============================================================

// ListGroups 返回所有 RSS 分组列表，按排序序号排列。
func (s *RSSService) ListGroups() ([]model.RSSGroup, error) {
	var groups []model.RSSGroup
	if err := s.db.Order("sort_order ASC, id ASC").Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("查询分组列表失败: %w", err)
	}
	return groups, nil
}

// GetGroup 根据 ID 获取 RSS 分组（含关联订阅源）。
func (s *RSSService) GetGroup(id uint64) (*model.RSSGroup, error) {
	var group model.RSSGroup
	if err := s.db.Preload("Feeds").First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRSSFeedNotFound
		}
		return nil, fmt.Errorf("查询分组失败: %w", err)
	}
	return &group, nil
}

// CreateGroup 创建新的 RSS 分组。
func (s *RSSService) CreateGroup(name string) (*model.RSSGroup, error) {
	if name == "" {
		return nil, errors.New("分组名称不能为空")
	}
	group := model.RSSGroup{Name: name}
	if err := s.db.Create(&group).Error; err != nil {
		return nil, fmt.Errorf("创建分组失败: %w", err)
	}
	return &group, nil
}

// UpdateGroup 更新 RSS 分组名称和排序。
func (s *RSSService) UpdateGroup(id uint64, name string, sortOrder *int) error {
	updates := map[string]interface{}{}
	if name != "" {
		updates["name"] = name
	}
	if sortOrder != nil {
		updates["sort_order"] = *sortOrder
	}
	if len(updates) == 0 {
		return nil
	}
	result := s.db.Model(&model.RSSGroup{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("更新分组失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrRSSFeedNotFound
	}
	return nil
}

// DeleteGroup 删除 RSS 分组（关联的订阅源移到未分组）。
func (s *RSSService) DeleteGroup(id uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 将分组下的订阅源移到未分组
		if err := tx.Model(&model.RSSFeed{}).Where("group_id = ?", id).Update("group_id", nil).Error; err != nil {
			return fmt.Errorf("移动订阅源失败: %w", err)
		}
		result := tx.Delete(&model.RSSGroup{}, id)
		if result.Error != nil {
			return fmt.Errorf("删除分组失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrRSSFeedNotFound
		}
		return nil
	})
}

// magnetPattern 匹配 magnet: 协议链接。
var magnetPattern = regexp.MustCompile(`magnet:\?xt=urn:btih:[a-zA-Z0-9]+[^\s"<]*`)

// extractMagnet 从文本中提取第一个磁力链接。
func extractMagnet(text string) string {
	return magnetPattern.FindString(text)
}

// truncateStr 按字符截断字符串，用于保护数据库列长度限制。
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// rssImgPattern 匹配 HTML 中的 <img> 标签。
var rssImgPattern = regexp.MustCompile(`<img[^>]+src=["']([^"']+)["'][^>]*/?>`)

// cacheDir 图片缓存目录。
var cacheDir = filepath.Join("web", "static", "uploads", "rss_img")

// dangerousSelfClosing 自闭合或单标签形式的危险标签。
var dangerousSelfClosing = regexp.MustCompile(`<(script|iframe|link|embed|object|frame)\b[^>]*/?>`)

// dangerousPaired 成对出现的危险标签（含内容），逐标签匹配（RE2 不支持 backreference）。
var dangerousPaired = regexp.MustCompile(`(?s)<script\b[^>]*>.*?</script>|<iframe\b[^>]*>.*?</iframe>|<embed\b[^>]*>.*?</embed>|<object\b[^>]*>.*?</object>|<frame\b[^>]*>.*?</frame>`)

// extSrcPattern 任意标签中带外部 URL 的 src 属性。
var extSrcPattern = regexp.MustCompile(`src=["'](https?://[^"']+)["']`)

// styleUrlPattern 内联 style 中的外链图片（background-image 等）。
var styleUrlPattern = regexp.MustCompile(`url\(\s*["']?(https?://[^)"'\s]+)["']?\s*\)`)

// imgTagPattern 匹配完整的 <img> 标签。
var imgTagPattern = regexp.MustCompile(`(?i)<img\b[^>]*>`)

// protectImgTags 把 <img> 标签替换为占位符，返回替换后的内容与还原表。
//
// 用途：后续要剥离"所有外部 src"，但 img 是允许保留的 —— 关闭图片缓存时
// 仍需让浏览器直接加载原图。占位符用 NUL 包裹，正常 HTML 里不会出现。
func protectImgTags(content string) (string, map[string]string) {
	restore := make(map[string]string)
	i := 0
	out := imgTagPattern.ReplaceAllStringFunc(content, func(tag string) string {
		key := fmt.Sprintf("\x00IMG%d\x00", i)
		restore[key] = tag
		i++
		return key
	})
	return out, restore
}

// restoreImgTags 把占位符还原为原始 <img> 标签。
func restoreImgTags(content string, restore map[string]string) string {
	for key, tag := range restore {
		content = strings.ReplaceAll(content, key, tag)
	}
	return content
}

// sanitizeContent 清洗 RSS 内容：移除危险标签、缓存外链图片、剥离其他外部资源。
//
// 图片缓存是抓取耗时的主要来源：一个含 20 张图的条目若串行下载，最坏要
// 等 20 × 5s。这里改为"先收集、并发下载、再统一替换"，并用 ctx 作为
// 总体预算 —— 预算耗尽的图片直接放弃，不拖住入库。
//
// rss.image_cache_enabled=false 时完全跳过下载，图片保留原始外链由
// 浏览器直接加载：抓取速度显著提升，也不占用本地磁盘。
func (s *RSSService) sanitizeContent(ctx context.Context, feedID uint64, entryLink, content string) string {
	if content == "" {
		return content
	}

	// 1. 移除危险标签
	content = dangerousSelfClosing.ReplaceAllString(content, "")
	content = dangerousPaired.ReplaceAllString(content, "")

	// 2. <img> 外部图片 → 本地缓存（关闭缓存时保持原样）
	content = s.cacheContentImages(ctx, feedID, entryLink, content)

	// 3. 保护 <img> 标签：下面要剥离的"外部 src"不应波及图片本身
	content, restore := protectImgTags(content)

	// 4. 其他标签的外部 src → 移除 src 属性
	content = extSrcPattern.ReplaceAllString(content, "")

	// 5. 内联 style 中的外链 url(...) → 移除 url 的值（保留占位）
	content = styleUrlPattern.ReplaceAllString(content, "none")

	return restoreImgTags(content, restore)
}

// imageCacheEnabled 读取图片缓存开关（配置热更新即时生效）。
func imageCacheEnabled() bool {
	cfg := config.Get()
	if cfg == nil {
		return true
	}
	return cfg.RSS.ImageCacheEnabled
}

// cacheContentImages 把内容里的外链图片缓存到本地并改写为站内 URL。
//
// 具体下载、类型/大小校验、归属登记与去重都交给 RSSImageCache；
// 本函数只负责从 HTML 中收集 URL、调用缓存、替换标签。
func (s *RSSService) cacheContentImages(ctx context.Context, feedID uint64, entryLink, content string) string {
	if !imageCacheEnabled() {
		return content
	}

	// 收集去重后的待缓存 URL
	seen := make(map[string]bool)
	var pending []string
	for _, m := range rssImgPattern.FindAllStringSubmatch(content, -1) {
		if len(m) < 2 || m[1] == "" {
			continue
		}
		src := m[1]
		if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "data:") || seen[src] {
			continue
		}
		seen[src] = true
		pending = append(pending, src)
	}
	if len(pending) == 0 {
		return content
	}

	localURL := s.imageCache().Cache(ctx, feedID, entryLink, pending)

	// 统一替换：成功改写的用站内 URL，失败/跳过的图片标签整体移除。
	return rssImgPattern.ReplaceAllStringFunc(content, func(tag string) string {
		m := rssImgPattern.FindStringSubmatch(tag)
		if len(m) < 2 || m[1] == "" {
			return tag
		}
		src := m[1]
		if u, ok := localURL[src]; ok {
			return strings.Replace(tag, src, u, 1)
		}
		// 只删除真正尝试过缓存的失败图片；相对路径 / data URL 等
		// 本来就没列入 pending，应保留原样。
		if seen[src] {
			return ""
		}
		return tag
	})
}

// isImageExt 判断扩展名是否为常见图片格式。
func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico":
		return true
	}
	return false
}
