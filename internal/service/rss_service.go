package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/imroc/req/v3"
	"github.com/mmcdole/gofeed"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RSS 订阅相关业务错误。
var (
	ErrRSSFeedNotFound = errors.New("订阅源不存在")
	ErrRSSFeedExists   = errors.New("订阅源已存在")
)

const rssHTTPTimeout = 30 * time.Second

// RSSService RSS 订阅业务服务。
//
// 负责：
//   - RSS 订阅源的增删改查
//   - RSS 条目的分页查询
//   - 定时抓取 RSS 内容并入库（支持代理）
//   - 条目保留数量控制
type RSSService struct {
	db         *gorm.DB
	httpClient *req.Client
}

// NewRSSService 创建 RSSService 实例。
func NewRSSService(db *gorm.DB) *RSSService {
	return &RSSService{
		db:         db,
		httpClient: buildRSSHTTPClient(),
	}
}

// ============================================================
// 订阅源 CRUD
// ============================================================

// CreateFeed 创建新的 RSS 订阅源。
//
// 先检查 URL 是否已存在，然后通过 gofeed 解析订阅源获取元数据，
// 最后创建 RSSFeed 记录并返回。
func (s *RSSService) CreateFeed(urlStr string) (*model.RSSFeed, error) {
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

	// 解析订阅源获取元数据
	feed, err := s.fetchAndParse(urlStr)
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

	// 提取站点图标
	if feed.Image != nil && feed.Image.URL != "" {
		rssFeed.IconURL = feed.Image.URL
	}

	if err := s.db.Create(&rssFeed).Error; err != nil {
		return nil, fmt.Errorf("创建订阅源失败: %w", err)
	}

	return &rssFeed, nil
}

// UpdateFeed 更新订阅源标题和抓取间隔。
func (s *RSSService) UpdateFeed(id uint64, title string, fetchInterval int) error {
	updates := map[string]interface{}{}
	if title != "" {
		updates["title"] = title
	}
	if fetchInterval > 0 {
		updates["fetch_interval"] = fetchInterval
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

// DeleteFeed 删除订阅源及其所有条目。
func (s *RSSService) DeleteFeed(id uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 先删所有条目
		if err := tx.Where("feed_id = ?", id).Delete(&model.RSSEntry{}).Error; err != nil {
			return fmt.Errorf("删除订阅条目失败: %w", err)
		}
		// 再删订阅源
		result := tx.Delete(&model.RSSFeed{}, id)
		if result.Error != nil {
			return fmt.Errorf("删除订阅源失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrRSSFeedNotFound
		}
		return nil
	})
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

// ListFeeds 返回所有订阅源列表。
func (s *RSSService) ListFeeds() ([]model.RSSFeed, error) {
	var feeds []model.RSSFeed
	if err := s.db.Order("created_at DESC").Find(&feeds).Error; err != nil {
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
// 返回条目列表、总数和错误。
func (s *RSSService) ListEntries(feedID uint64, page, perPage int) ([]model.RSSEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 20
	}

	var total int64
	if err := s.db.Model(&model.RSSEntry{}).Where("feed_id = ?", feedID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计条目数量失败: %w", err)
	}

	var entries []model.RSSEntry
	offset := (page - 1) * perPage
	if err := s.db.Where("feed_id = ?", feedID).
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
func (s *RSSService) FetchFeed(id uint64) error {
	var feed model.RSSFeed
	if err := s.db.First(&feed, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRSSFeedNotFound
		}
		return fmt.Errorf("查询订阅源失败: %w", err)
	}

	// 抓取并解析
	parsed, err := s.fetchAndParse(feed.URL)
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
		entry := s.itemToEntry(feed.ID, item, now)
		if entry.Link == "" {
			continue // 跳过无链接条目
		}
		entries = append(entries, entry)
	}

	// 批量插入（ON CONFLICT DO NOTHING）
	if len(entries) > 0 {
		if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error; err != nil {
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

	// 执行保留策略
	if err := s.enforceRetention(feed.ID); err != nil {
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

// FetchAll 抓取所有到期的订阅源。
//
// 到期判定：now - LastFetch > FetchInterval（秒）。
func (s *RSSService) FetchAll() error {
	var feeds []model.RSSFeed
	if err := s.db.Find(&feeds).Error; err != nil {
		return fmt.Errorf("查询订阅源列表失败: %w", err)
	}

	now := time.Now()
	var lastErr error
	for _, feed := range feeds {
		// 判断是否到期
		interval := feed.FetchInterval
		if interval <= 0 {
			interval = 3600
		}
		if now.Sub(feed.LastFetch) <= time.Duration(interval)*time.Second {
			continue
		}

		if err := s.FetchFeed(feed.ID); err != nil {
			lastErr = err
			// 继续处理下一个，不因单个失败中断
		}
	}

	return lastErr
}

// StartBackgroundFetcher 启动 RSS 后台定时抓取 goroutine。
//
// 定期读取当前配置，根据 background_fetch_enabled 和 background_fetch_interval
// 决定是否抓取。配置热更新后下一轮自动生效，无需重启服务。
// ctx 用于优雅退出。
func (s *RSSService) StartBackgroundFetcher(ctx context.Context) {
	defaultInterval := 300

	go func() {
		for {
			cfg := config.Get()
			enabled := cfg != nil && cfg.RSS.BackgroundFetchEnabled
			interval := defaultInterval
			if cfg != nil && cfg.RSS.BackgroundFetchInterval > 0 {
				interval = cfg.RSS.BackgroundFetchInterval
			}

			if enabled {
				if err := s.FetchAll(); err != nil {
					log.Printf("[rss] 后台定时抓取出错: %v", err)
				}
			}

			select {
			case <-ctx.Done():
				log.Println("[rss] 后台定时抓取已停止")
				return
			case <-time.After(time.Duration(interval) * time.Second):
			}
		}
	}()
}

// ============================================================
// 内部方法
// ============================================================

// fetchAndParse 通过 HTTP 客户端获取 RSS 内容并用 gofeed 解析。
func (s *RSSService) fetchAndParse(urlStr string) (*gofeed.Feed, error) {
	resp, err := s.httpClient.R().Get(urlStr)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP 状态码 %d", resp.StatusCode)
	}

	parser := gofeed.NewParser()
	feed, err := parser.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RSS 解析失败: %w", err)
	}

	return feed, nil
}

// itemToEntry 将 gofeed 条目转换为 RSSEntry 模型。
func (s *RSSService) itemToEntry(feedID uint64, item *gofeed.Item, fetchedAt time.Time) model.RSSEntry {
	entry := model.RSSEntry{
		FeedID:    feedID,
		Title:     item.Title,
		Link:      item.Link,
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
	entry.Content = content

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
// 从配置读取 retention_count（默认 5000），
// 超出限制时删除最旧的条目。
func (s *RSSService) enforceRetention(feedID uint64) error {
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

	// 删除超出保留数量的最旧条目
	excess := int(count) - retention
	if err := s.db.Where("feed_id = ?", feedID).
		Where("id IN (?)",
			s.db.Model(&model.RSSEntry{}).
				Select("id").
				Where("feed_id = ?", feedID).
				Order("published ASC").
				Limit(excess),
		).Delete(&model.RSSEntry{}).Error; err != nil {
		return fmt.Errorf("清理旧条目失败: %w", err)
	}

	return nil
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
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("禁止访问内部地址: %s", host)
		}
		if ip.IsPrivate() {
			return fmt.Errorf("禁止访问内网地址: %s", host)
		}
		// 云元数据端点
		if ip.Equal(net.IPv4(169, 254, 169, 254)) {
			return fmt.Errorf("禁止访问云元数据端点: %s", host)
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

// buildRSSHTTPClient 根据 RSS 代理配置构建 HTTP 客户端。
//
// 通过 config.ResolveProxy 解析 RSS 模块的代理配置，
// 支持 HTTP 代理。配置为空时直连。
func buildRSSHTTPClient() *req.Client {
	transport := &http.Transport{}

	cfg := config.Get()
	if cfg != nil {
		proxyURLs := config.ResolveProxy(cfg.RSS.ProxyServiceConfig, "")
		if len(proxyURLs) > 0 {
			proxyAddr := proxyURLs[0]
			transport.Proxy = func(_ *http.Request) (*url.URL, error) {
				return url.Parse(proxyAddr)
			}
		}
	}

	client := req.C().SetTimeout(rssHTTPTimeout)
	client.GetClient().Transport = transport
	return client
}
