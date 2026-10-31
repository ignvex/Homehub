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
	ErrRSSFeedNotFound  = errors.New("订阅源不存在")
	ErrRSSFeedExists    = errors.New("订阅源已存在")
	ErrRSSEntryNotFound = errors.New("条目不存在")
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

	// pikpak 是磁力链接提交的客户端；nil 表示未启用。
	// 只在 SubmitMagnetToPikPak 里用，不影响抓取路径。
	pikpak PikPakClient
}

// NewRSSService 创建 RSSService 实例。
//
// 默认抓取器跟随配置：rss.extension_url 为空时本地抓取，否则交给
// extension 服务（HomeHub 单向发起请求）。要完全接管，可调用 SetFetcher。
func NewRSSService(db *gorm.DB) *RSSService {
	svc := &RSSService{
		db:      db,
		fetcher: newAutoFetcher(),
		images:  NewLocalImageCache(db),
	}
	// PikPak 客户端跟随配置创建：未启用时是 nil，
	// SubmitMagnetToPikPak 会返回 ErrPikPakDisabled。
	if cfg := config.Get(); cfg != nil {
		svc.pikpak = NewPikPakClient(cfg.RSS.PikPak)
	}
	return svc
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

	// 构造 RSSFeed 记录。
	//
	// 标题、站点地址、图标都来自外部 feed，长度不受本仓库控制，必须按列
	// 定义裁剪：超过 varchar 上限会让 PostgreSQL 拒绝整条 INSERT。
	// URL 与条目 link 一样带 btree 唯一索引，额外受索引单键字节上限约束。
	rssFeed := model.RSSFeed{
		URL:           model.TruncateIndexed(urlStr, 2048),
		Title:         model.TruncateRunes(feed.Title, 255),
		SiteURL:       model.TruncateIndexed(feed.Link, 2048),
		Description:   feed.Description,
		FetchInterval: 3600, // 默认 1 小时
	}

	if groupID != nil {
		rssFeed.GroupID = groupID
	}

	// 提取站点图标
	if feed.Image != nil && feed.Image.URL != "" {
		rssFeed.IconURL = model.TruncateIndexed(feed.Image.URL, 2048)
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

// GroupUpdate 表达对订阅源 group_id 的更新意图。
//
// 三种状态必须分开：
//   - Set=false             请求里没带 group_id，保持原值
//   - Set=true, Value=nil   显式清空分组（写入 NULL）
//   - Set=true, Value!=nil  移入指定分组
//
// 只用 *uint64 无法区分前两者：JSON 字段缺失和显式 null 都会绑成 nil。
// 那正是“选未分组清不掉”的成因 —— 界面点了没反应，也不报错。
type GroupUpdate struct {
	Set   bool
	Value *uint64
}

// buildFeedUpdates 把可选的更新意图翻译成 UPDATE 的 SET 子句。
//
// 空值表示“不改这个字段”，所以 title 为空、interval 非正、Set=false
// 都各自跳过；而 Set=true 且 Value=nil 是要真的写入 NULL。
func buildFeedUpdates(title string, fetchInterval int, group GroupUpdate) map[string]interface{} {
	updates := map[string]interface{}{}
	if title != "" {
		updates["title"] = model.TruncateRunes(title, 255)
	}
	if fetchInterval > 0 {
		updates["fetch_interval"] = fetchInterval
	}
	if group.Set {
		if group.Value == nil {
			updates["group_id"] = nil
		} else {
			updates["group_id"] = *group.Value
		}
	}
	return updates
}

// UpdateFeed 更新订阅源标题、抓取间隔和所属分组。
func (s *RSSService) UpdateFeed(id uint64, title string, fetchInterval int, group GroupUpdate) error {
	updates := buildFeedUpdates(title, fetchInterval, group)
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

// DeleteFeed 删除订阅源。
//
// 条目是跨源共享的，因此这里只删除"该源收录了哪些条目"的关联行；
// 只有当一个条目不再被任何源收录时，才连同它的图片引用一起回收。
// 换句话说：删 A 源绝不会带走 B 源仍在用的内容。
func (s *RSSService) DeleteFeed(id uint64) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var feed model.RSSFeed
		if err := tx.First(&feed, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRSSFeedNotFound
			}
			return fmt.Errorf("查询订阅源失败: %w", err)
		}

		// 先记下该源收录的条目，删完关联后据此判断谁成了孤儿。
		var entryIDs []uint64
		if err := tx.Model(&model.RSSEntryFeed{}).
			Where("feed_id = ?", id).
			Pluck("entry_id", &entryIDs).Error; err != nil {
			return fmt.Errorf("查询订阅源条目失败: %w", err)
		}

		if err := tx.Unscoped().Where("feed_id = ?", id).Delete(&model.RSSEntryFeed{}).Error; err != nil {
			return fmt.Errorf("删除订阅源条目关联失败: %w", err)
		}
		if err := tx.Unscoped().Delete(&model.RSSFeed{}, id).Error; err != nil {
			return fmt.Errorf("删除订阅源失败: %w", err)
		}

		return s.purgeOrphanEntries(tx, entryIDs)
	})
	if err != nil {
		return err
	}

	s.sweepImages(context.Background(), 0)
	return nil
}

// purgeOrphanEntries 删除不再被任何源收录的条目及其图片引用。
//
// 传入的 entryIDs 只是候选集：其他源可能仍在引用其中一部分，所以必须
// 按"当前是否还有关联行"重新判定，不能按候选集直接删。
//
// 判定与删除必须在**同一条语句**里完成。早期写法是先 SELECT 出 orphan
// 列表、再 DELETE WHERE id IN (orphans)：READ COMMITTED 下两条语句各取
// 一次快照，若另一源的抓取恰好在两者之间提交了该条目的关联，DELETE 不会
// 重新评估 NOT EXISTS —— 条目连同正文与图片引用被删掉，只留下一条悬挂
// 关联；随后 Sweep 会把失去引用的图片文件也清掉，等于把别源的正常内容
// 连根删除。
//
// 现在用带 NOT EXISTS 的 DELETE ... RETURNING，判定和删除是同一个快照。
func (s *RSSService) purgeOrphanEntries(tx *gorm.DB, entryIDs []uint64) error {
	if len(entryIDs) == 0 {
		return nil
	}

	// 物理删除：软删除会让 link 唯一索引一直占位，同一链接重新出现时
	// 插入会被 ON CONFLICT 静默吞掉。
	//
	// RETURNING id 拿回真正被删的行，供下面清理图片引用 —— 不能再靠
	// 事前的候选集推断哪些条目真的死了。
	var orphans []uint64
	if err := tx.Raw(`
		DELETE FROM rss_entries
		WHERE id IN ?
		  AND NOT EXISTS (
		      SELECT 1 FROM rss_entry_feeds ef
		      WHERE ef.entry_id = rss_entries.id AND ef.deleted_at IS NULL
		  )
		RETURNING id`, entryIDs).Scan(&orphans).Error; err != nil {
		return fmt.Errorf("删除孤儿条目失败: %w", err)
	}
	if len(orphans) == 0 {
		return nil
	}

	// 图片引用指向条目，条目已删，引用一并回收。
	if err := tx.Unscoped().Where("entry_id IN ?", orphans).Delete(&model.RSSImageRef{}).Error; err != nil {
		return fmt.Errorf("删除孤儿条目的图片引用失败: %w", err)
	}
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

// storedEntry 是条目入库后的最小现状：主键、链接、是否已有正文。
type storedEntry struct {
	ID      uint64
	Link    string
	Content string
}

// RSSEntryView 是条目列表的展示结构。
//
// 条目本身不归属任何源，FeedIDs 列出收录它的全部源；Published 取该源
// 给出的发布时间（按源筛选时），未筛选时取条目首次发布时间。
type RSSEntryView struct {
	ID        uint64    `json:"id"`
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Author    string    `json:"author,omitempty"`
	Content   string    `json:"content,omitempty"`
	MagnetURL string    `json:"magnet_url,omitempty"`
	Published time.Time `json:"published"`
	FetchedAt time.Time `json:"fetched_at"`
	FeedIDs   []uint64  `json:"feed_ids"`

	MagnetLocked   bool       `json:"magnet_locked"`
	MagnetLockedAt *time.Time `json:"magnet_locked_at,omitempty"`
	PikPakStatus   string     `json:"pikpak_status,omitempty"`
	PikPakTaskID   string     `json:"pikpak_task_id,omitempty"`
	PikPakError    string     `json:"pikpak_error,omitempty"`
	PikPakSavedAt  *time.Time `json:"pikpak_saved_at,omitempty"`
}

// EntryFilter 是条目列表的查询条件。
//
// 用结构体而不是一长串参数：筛选维度还会继续增加（已锁、已提交、
// 有磁力……），位置参数每加一个都会让调用点变得难以阅读。
type EntryFilter struct {
	FeedID  uint64
	GroupID uint64
	Page    int
	PerPage int
	// Magnet 磁力链接维度筛选：
	//   ""          不过滤
	//   "has"       有磁力链接
	//   "locked"    已锁定
	//   "unlocked"  有磁力但未锁定
	//   "pikpak_saved"  已成功提交到 PikPak
	//   "pikpak_failed" 提交失败
	Magnet string
}

// ListEntries 按条件分页查询条目，按发布时间倒序。
//
// 同一个条目被多个源收录时只出现一次，FeedIDs 列出全部来源。
func (s *RSSService) ListEntries(f EntryFilter) ([]RSSEntryView, int64, error) {
	page, perPage := f.Page, f.PerPage
	feedID, groupID := f.FeedID, f.GroupID

	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 20
	}

	baseQuery := s.db.Table("rss_entries AS e").
		Joins("JOIN rss_entry_feeds AS ef ON ef.entry_id = e.id AND ef.deleted_at IS NULL").
		Where("e.deleted_at IS NULL")

	if feedID > 0 {
		baseQuery = baseQuery.Where("ef.feed_id = ?", feedID)
	}
	if groupID > 0 {
		baseQuery = baseQuery.
			Joins("JOIN rss_feeds AS f ON f.id = ef.feed_id AND f.deleted_at IS NULL").
			Where("f.group_id = ?", groupID)
	}

	// 磁力链接维度。condition 为 nil 时表示参数值非法 —— 调用方应先校验，
	// 这里不再兜底成“忽略筛选”，否则非法值会静默返回全部数据。
	if cond, ok := magnetCondition(f.Magnet); !ok {
		return nil, 0, fmt.Errorf("无效的磁力筛选条件 %q", f.Magnet)
	} else if cond != "" {
		baseQuery = baseQuery.Where(cond)
	}

	// 统计总数用独立会话。
	//
	// 直接 baseQuery.Distinct(...).Count(...) 会把 DISTINCT 写进 baseQuery
	// 自身，随后的分页查询就变成 `SELECT DISTINCT e.id ... GROUP BY e.id,
	// e.published ORDER BY e.published`，PostgreSQL 会报
	// 42P10：ORDER BY 表达式必须出现在 SELECT 列表里。
	// Session 复制一份 statement，条件相同但不互相污染。
	var total int64
	if err := baseQuery.Session(&gorm.Session{}).Distinct("e.id").Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计条目数量失败: %w", err)
	}
	if total == 0 {
		return []RSSEntryView{}, 0, nil
	}

	// 分两步：先只取主键分页，再按主键取正文。
	//
	// 一步写完需要 GROUP BY 全部展示列，而 content 是 text —— PostgreSQL
	// 无法用索引完成这种分组，会退化成全量排序。只分页主键则可以走
	// entry_feeds 上的索引。
	idQuery := baseQuery.Select("e.id")
	if feedID > 0 {
		// 按源筛选时用该源给出的时间排序。
		idQuery = idQuery.
			Group("e.id").
			Order("MIN(ef.published) DESC, e.id DESC")
	} else {
		idQuery = idQuery.
			Group("e.id, e.published").
			Order("e.published DESC, e.id DESC")
	}

	var ids []uint64
	offset := (page - 1) * perPage
	if err := idQuery.Offset(offset).Limit(perPage).Pluck("e.id", &ids).Error; err != nil {
		return nil, 0, fmt.Errorf("查询条目列表失败: %w", err)
	}
	if len(ids) == 0 {
		return []RSSEntryView{}, total, nil
	}

	var entries []model.RSSEntry
	if err := s.db.Where("id IN ?", ids).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("查询条目详情失败: %w", err)
	}
	byID := make(map[uint64]model.RSSEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}

	// 一次性补齐这些条目的全部来源，避免 N+1。
	var links []model.RSSEntryFeed
	if err := s.db.Select("entry_id", "feed_id").
		Where("entry_id IN ?", ids).
		Order("feed_id ASC").
		Find(&links).Error; err != nil {
		return nil, 0, fmt.Errorf("查询条目来源失败: %w", err)
	}
	feedsByEntry := make(map[uint64][]uint64, len(links))
	for _, link := range links {
		feedsByEntry[link.EntryID] = append(feedsByEntry[link.EntryID], link.FeedID)
	}

	// 按分页顺序输出：IN 查询不保证顺序。
	views := make([]RSSEntryView, 0, len(ids))
	for _, id := range ids {
		entry, ok := byID[id]
		if !ok {
			continue
		}
		feedIDs := feedsByEntry[id]
		if feedIDs == nil {
			feedIDs = []uint64{}
		}
		views = append(views, RSSEntryView{
			ID:             entry.ID,
			Title:          entry.Title,
			Link:           entry.Link,
			Author:         entry.Author,
			Content:        entry.Content,
			MagnetURL:      entry.MagnetURL,
			Published:      entry.Published,
			FetchedAt:      entry.FetchedAt,
			FeedIDs:        feedIDs,
			MagnetLocked:   entry.MagnetLocked,
			MagnetLockedAt: entry.MagnetLockedAt,
			PikPakStatus:   entry.PikPakStatus,
			PikPakTaskID:   entry.PikPakTaskID,
			PikPakError:    entry.PikPakError,
			PikPakSavedAt:  entry.PikPakSavedAt,
		})
	}
	return views, total, nil
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
		// error_msg 是 varchar(1024)：按字符裁剪，字节切片会切断多字节字符。
		// 外部响应片段可能自带非法 UTF-8（512 字节硬切），TruncateEllipsis
		// 会一并修复，否则整条 UPDATE 会被 PostgreSQL 拒收。
		errMsg := model.TruncateEllipsis(err.Error(), 1024)
		if dbErr := s.db.Model(&feed).Updates(map[string]interface{}{
			"last_fetch": time.Now(),
			"error_msg":  errMsg,
		}).Error; dbErr != nil {
			logger.Warn("[rss] 写入抓取失败状态失败", "feed_id", feed.ID, "error", dbErr)
		}
		return fmt.Errorf("抓取订阅源失败: %w", err)
	}

	// 1. 先把条目骨架写库（不含正文），拿到 entry ID。
	//
	// 条目按 link 全局唯一：B 源收录一条已被 A 源收录的内容时命中
	// ON CONFLICT 只更新磁力链接，不覆盖正文、也不改动归属 —— 归属由
	// 后面的 entry_feeds 关联行表达。
	now := time.Now()
	entries, publishedByLink := s.entriesFromItems(parsed.Items, now)

	if len(entries) > 0 {
		// 冲突时只回填 magnet_url，且不用空值覆盖已有值。
		//
		// 背景：条目跨源共享，同一个 link 可能先被带磁力的源收录、
		// 再被不带磁力的源收录。直接用 `SET magnet_url = excluded.magnet_url`
		// 会在后一次抓取时把已有磁力链接清空 —— 一次抓取就永久丢失
		// 已经提取到的数据，而正文走的是"首次写入优先"，磁力却相反。
		if err := s.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "link"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				// 三层保护，优先级从高到低：
				//   1. 已锁定 → 一个字都不改（人工确认过的链接最高优先级）；
				//   2. 新值为空 → 保留旧值（避免一次抓取清空已有链接）；
				//   3. 否则用新值。
				"magnet_url": gorm.Expr(
					`CASE WHEN rss_entries.magnet_locked
					      THEN rss_entries.magnet_url
					      ELSE COALESCE(NULLIF(excluded.magnet_url, ''), rss_entries.magnet_url)
					 END`),
			}),
		}).Create(&entries).Error; err != nil {
			return s.failFeed(feed, now, fmt.Errorf("保存条目失败: %w", err))
		}
	}

	// 2. 查回 link → (id, 正文是否为空)，用于补正文与建立关联。
	links := make([]string, 0, len(publishedByLink))
	for link := range publishedByLink {
		links = append(links, link)
	}

	var stored []storedEntry
	if len(links) > 0 {
		if err := s.db.Model(&model.RSSEntry{}).
			Select("id", "link", "content").
			Where("link IN ?", links).
			Find(&stored).Error; err != nil {
			return s.failFeed(feed, now, fmt.Errorf("查询条目失败: %w", err))
		}
	}

	// 3. 只为正文为空的条目做清洗与图片缓存。
	//
	// 已有正文说明这条内容在别的源（或更早的抓取）已经处理过：重复下载
	// 图片没有意义，也避免用较短的摘要覆盖别处更完整的正文。
	if err := s.fillEntryContent(ctx, parsed, stored); err != nil {
		return s.failFeed(feed, now, err)
	}

	// 4. 建立"本源收录了这些条目"的关联。
	if len(stored) > 0 {
		// 先复验订阅源仍然存在。
		//
		// FetchFeed 在第 1 步读出 feed 行之后，要花数秒到数分钟做 HTTP
		// 抓取与图片下载。这期间用户完全可能删掉这个源："加错源，几秒内
		// 删掉"就是最常见的一条路径（CreateFeed 会用独立 goroutine 立即
		// 首次抓取）。删除事务已经提交，若这里不检查就写入，会留下指向
		// 不存在 feed 的悬挂关联。
		//
		// 悬挂关联的后果比"多几行垃圾"严重得多：孤儿判定只看关联行是否
		// 存在（NOT EXISTS），所以这些条目永远不满足孤儿条件——正文、
		// 图片引用、磁盘上的图片文件全被永久钉住，且没有任何回收路径。
		//
		// Model(&RSSFeed{}) 会带上 deleted_at IS NULL，软删除的源同样算不存在。
		var alive int64
		if err := s.db.Model(&model.RSSFeed{}).Where("id = ?", feed.ID).
			Count(&alive).Error; err != nil {
			return s.failFeed(feed, now, fmt.Errorf("确认订阅源存在性失败: %w", err))
		}
		if alive == 0 {
			// 源已被删除：不写关联，并回收本批刚插入却已无归属的条目。
			//
			// 只跳过关联是不够的。条目、正文与图片引用在走到这里之前
			// 已经写库（FetchFeed 全程不在一个事务里），若不复回收，
			// 留下来的是"无关联条目"——而孤儿回收的候选集只来自
			// entry_feeds，这类条目永远不会成为候选，等于永久泄漏。
			//
			// purgeOrphanEntries 只删确实没有任何关联的条目：本批中如果
			// 有内容早已被其他源收录，它们会被安全保留。
			ids := make([]uint64, 0, len(stored))
			for _, entry := range stored {
				ids = append(ids, entry.ID)
			}
			if err := s.purgeOrphanEntries(s.db, ids); err != nil {
				return s.failFeed(feed, now, fmt.Errorf("回收已删除订阅源的孤儿条目失败: %w", err))
			}
			logger.Info("[rss] 订阅源在抓取期间被删除，已丢弃本批关联并回收孤儿条目",
				"feed_id", feed.ID, "candidates", len(ids))
			return nil
		}

		refs := make([]model.RSSEntryFeed, 0, len(stored))
		for _, entry := range stored {
			published := publishedByLink[entry.Link]
			if published.IsZero() {
				published = now
			}
			refs = append(refs, model.RSSEntryFeed{
				EntryID:     entry.ID,
				FeedID:      feed.ID,
				Published:   published,
				FirstSeenAt: now,
				LastSeenAt:  now,
			})
		}
		if err := s.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "entry_id"}, {Name: "feed_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"published", "last_seen_at", "updated_at",
			}),
		}).Create(&refs).Error; err != nil {
			return s.failFeed(feed, now, fmt.Errorf("保存条目归属失败: %w", err))
		}
	}

	logger.Info("[rss] 抓取完成", "feed_id", feed.ID, "entries", len(entries))

	// 4.5 自动锁定磁力链接（配置开启时）
	if err := s.autoLockMagnets(now); err != nil {
		// 只是辅助功能，失败不该让整轮抓取失败
		logger.Warn("[rss] 自动锁定磁力链接失败", "feed_id", feed.ID, "error", err)
	}

	// 5. 执行保留策略
	if err := s.enforceRetention(ctx, feed.ID); err != nil {
		return s.failFeed(feed, now, err)
	}

	// 更新成功状态
	updates := map[string]interface{}{
		"last_fetch": now,
		"error_msg":  "",
	}
	// 如果标题发生了变化，更新标题（同样按列定义裁剪）
	if parsed.Title != "" && parsed.Title != feed.Title {
		updates["title"] = model.TruncateRunes(parsed.Title, 255)
	}
	if parsed.Description != "" {
		updates["description"] = parsed.Description // text 列，无长度上限
	}
	if parsed.Link != "" {
		updates["site_url"] = model.TruncateIndexed(parsed.Link, 2048)
	}
	if parsed.Image != nil && parsed.Image.URL != "" {
		updates["icon_url"] = model.TruncateIndexed(parsed.Image.URL, 2048)
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

// entriesFromItems 把解析出的条目转成待入库的行，并按 link 去重。
//
// 去重是必需的，不是优化：调用方会把返回值作为**一条多行 INSERT ...
// ON CONFLICT DO UPDATE** 的输入。PostgreSQL 不充许同一语句内有两个
// 行命中同一个冲突键，会直接报错：
//
//	21000: ON CONFLICT DO UPDATE command cannot affect row a second time
//
// 而同一个 feed 内出现两条相同 link 是常见现象（源站自己重复发布）；
// 链接被截断到列上限后，两个不同长 URL 也可能撞成同一个值。一旦发生，
// 不是丢一条而是**整批回滚** → 该源每一轮抓取都失败，且不会自愈。
//
// 保留首次出现的条目（与"首次写入优先"的正文策略一致），同时返回
// link → 发布时间 的映射供后续建立关联。
func (s *RSSService) entriesFromItems(items []*gofeed.Item, now time.Time) ([]model.RSSEntry, map[string]time.Time) {
	entries := make([]model.RSSEntry, 0, len(items))
	publishedByLink := make(map[string]time.Time, len(items))
	seen := make(map[string]struct{}, len(items))

	for _, item := range items {
		entry := s.itemToEntry(item, now)
		if entry.Link == "" {
			continue // 无链接的条目无法去重，也无法建立归属
		}
		if _, dup := seen[entry.Link]; dup {
			continue
		}
		seen[entry.Link] = struct{}{}
		entries = append(entries, entry)
		publishedByLink[entry.Link] = entry.Published
	}

	return entries, publishedByLink
}

// itemToEntry 把 gofeed 条目转换成不含正文的 RSSEntry 骨架。
//
// 正文要等条目入库、拿到 ID 之后才能清洗 —— 图片引用挂在 entry_id 上。
// 这里只处理不依赖 ID 的字段。
func (s *RSSService) itemToEntry(item *gofeed.Item, fetchedAt time.Time) model.RSSEntry {
	// 标题、作者、链接都直接来自外部 feed，长度不受本仓库控制，必须按列
	// 定义裁剪：超过 varchar 上限会让 PostgreSQL 拒绝整条 INSERT，而这批
	// 条目是一条 INSERT 写入的，一条超长会带走整批。截断点还必须落在 rune
	// 边界上，否则非法 UTF-8 同样会被拒收。
	// link 带 btree 唯一索引，额外受索引单键字节上限约束。
	entry := model.RSSEntry{
		Title:     model.TruncateRunes(item.Title, 500),
		Link:      model.TruncateIndexed(item.Link, 2048),
		FetchedAt: fetchedAt,
	}

	// 作者
	if item.Author != nil {
		entry.Author = model.TruncateRunes(item.Author.Name, 255)
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

// enforceRetention 执行保留策略。
//
// 计数单位是"本源收录了多少条"，而不是"库里有几条"：同一条内容被多个
// 源收录时，每个源各自保留自己的那份关联。清理时只删本源的关联行，
// 条目本身只有变成孤儿（不再被任何源收录）才回收。
func (s *RSSService) enforceRetention(ctx context.Context, feedID uint64) error {
	retention := 5000
	cfg := config.Get()
	if cfg != nil && cfg.RSS.RetentionCount > 0 {
		retention = cfg.RSS.RetentionCount
	}

	var count int64
	if err := s.db.Model(&model.RSSEntryFeed{}).Where("feed_id = ?", feedID).Count(&count).Error; err != nil {
		return fmt.Errorf("统计条目数量失败: %w", err)
	}
	if count <= int64(retention) {
		return nil
	}

	excess := int(count) - retention
	var stale []model.RSSEntryFeed
	if err := s.db.Select("id", "entry_id").
		Where("feed_id = ?", feedID).
		Order("published ASC, id ASC").
		Limit(excess).
		Find(&stale).Error; err != nil {
		return fmt.Errorf("查询待清理条目失败: %w", err)
	}
	if len(stale) == 0 {
		return nil
	}

	refIDs := make([]uint64, 0, len(stale))
	entryIDs := make([]uint64, 0, len(stale))
	for _, ref := range stale {
		refIDs = append(refIDs, ref.ID)
		entryIDs = append(entryIDs, ref.EntryID)
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("id IN ?", refIDs).Delete(&model.RSSEntryFeed{}).Error; err != nil {
			return fmt.Errorf("删除旧条目关联失败: %w", err)
		}
		return s.purgeOrphanEntries(tx, entryIDs)
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

// cacheDir 图片缓存目录。
var cacheDir = filepath.Join("web", "static", "uploads", "rss_img")

// sanitizeContent 清洗 RSS 正文并按需缓存图片。
//
// 解析交给 rss_sanitize.go 的白名单实现：危险标签整棵删除、未知标签保留
// 文字、属性按白名单过滤。这里只负责图片这一步 —— 先收集外链地址，
// 交给 RSSImageCache 并发下载，再用结果改写或删除 <img>。
//
// rss.image_cache_enabled=false 时跳过下载，图片保留原始外链由浏览器
// 直接加载：抓取速度显著提升，也不占用本地磁盘。
func (s *RSSService) sanitizeContent(ctx context.Context, entryID uint64, content string) string {
	if content == "" {
		return content
	}

	doc, ok := parseContent(content)
	if !ok {
		// 解析不出 HTML 时按纯文本输出，绝不原样透传。
		return escapeFallback(content)
	}

	if !imageCacheEnabled() {
		return doc.render()
	}

	if urls := doc.externalImageSources(); len(urls) > 0 {
		mapping, err := s.imageCache().Cache(ctx, entryID, urls)
		if err != nil {
			// 图片登记失败（数据库不可用等）：返回空串让调用方**不写正文**。
			//
			// 正文一旦落库就不会重算（fillEntryContent 只处理空正文）。
			// 若此时把“没有图的版本”写进去，这批图片就永久丢失，且没有任何
			// 自愈路径。保持正文为空，下次抓取会重新处理。
			//
			// 代价是这条条目暂时没有正文；比起永久丢图，这个代价更小，
			// 而且条目仍可通过 link 跳转原文。
			logger.Warn("[rss] 图片登记失败，暂不写正文以便下次重试",
				"entry_id", entryID, "error", err)
			return ""
		}
		doc.applyImageMapping(mapping)
	}
	return doc.render()
}

// failFeed 记录一次抓取失败的状态并返回包装后的错误。
func (s *RSSService) failFeed(feed model.RSSFeed, at time.Time, err error) error {
	// error_msg 是 varchar(1024)：按字符裁剪，字节切片会切断多字节字符。
	errMsg := model.TruncateEllipsis(err.Error(), 1024)
	if dbErr := s.db.Model(&feed).Updates(map[string]interface{}{
		"last_fetch": at,
		"error_msg":  errMsg,
	}).Error; dbErr != nil {
		// 不能默默丢掉：该 UPDATE 同时写 last_fetch，写不进去的话
		// FetchAll 会认为这个源永远到期，每轮都重试且每轮都不留痕迹，
		// 表现为"抓取一直失败，错误列却是空的"。此处只记一条日志，
		// 仍把原始错误返回给调用方。
		logger.Warn("[rss] 写入抓取失败状态失败", "feed_id", feed.ID, "error", dbErr)
	}
	return err
}

// fillEntryContent 为正文为空的条目清洗正文并缓存图片。
//
// 只有 content 为空的条目会被处理：已有正文说明这条内容已经处理过，
// 重复下载图片既浪费流量，也可能用较短的摘要覆盖别处更完整的正文。
func (s *RSSService) fillEntryContent(ctx context.Context, parsed *gofeed.Feed, stored []storedEntry) error {
	if len(stored) == 0 {
		return nil
	}

	// 按 link 索引源条目，避免嵌套遍历。
	itemsByLink := make(map[string]*gofeed.Item, len(parsed.Items))
	for _, item := range parsed.Items {
		// 必须与 itemToEntry 使用同一种截断，否则和库里的 link 匹配不上。
		link := model.TruncateIndexed(item.Link, 2048)
		if link == "" {
			continue
		}
		if _, exists := itemsByLink[link]; !exists {
			itemsByLink[link] = item
		}
	}

	for _, entry := range stored {
		if entry.Content != "" || entry.ID == 0 {
			continue
		}
		item, ok := itemsByLink[entry.Link]
		if !ok {
			continue
		}

		// 内容：优先摘要，再取正文，截断到 2000 字符
		raw := item.Description
		if raw == "" {
			raw = item.Content
		}
		if len(raw) > 2000 {
			runes := []rune(raw)
			if len(runes) > 2000 {
				raw = string(runes[:2000]) + "..."
			}
		}

		sanitized := s.sanitizeContent(ctx, entry.ID, raw)
		if err := s.db.Model(&model.RSSEntry{}).
			Where("id = ?", entry.ID).
			Update("content", sanitized).Error; err != nil {
			return fmt.Errorf("更新条目正文失败: %w", err)
		}
	}
	return nil
}

// imageCacheEnabled 读取图片缓存开关（配置热更新即时生效）。
func imageCacheEnabled() bool {
	cfg := config.Get()
	if cfg == nil {
		return true
	}
	return cfg.RSS.ImageCacheEnabled
}

// isImageExt 判断扩展名是否为常见图片格式。
func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico":
		return true
	}
	return false
}

// magnetCondition 把磁力筛选参数翻译成 SQL 条件。
//
// 第二个返回值 false 表示参数值非法。空串表示“不筛选”，是合法的。
// 白名单而不是拼接：这些值直接进 WHERE，不能接受任意输入。
func magnetCondition(mode string) (string, bool) {
	switch mode {
	case "":
		return "", true
	case "has":
		return "e.magnet_url <> ''", true
	case "locked":
		return "e.magnet_locked = TRUE AND e.magnet_url <> ''", true
	case "unlocked":
		return "e.magnet_locked = FALSE AND e.magnet_url <> ''", true
	case "pikpak_saved":
		return "e.pikpak_status = 'saved'", true
	case "pikpak_failed":
		return "e.pikpak_status = 'failed'", true
	default:
		return "", false
	}
}

// autoLockMagnets 把尚未锁定的、有磁力链接的条目自动标记为锁定。
//
// 只在 rss.pikpak.auto_lock 开启时生效。默认关闭是有意的：
// “锁定”这个动作的价值在于人工确认，自动锁定会让它退化成
// 一个无意义的布尔值。开启它的场景是“我信这个源，凡是带磁力的都算确认过”。
//
// MagnetLockedBy 记为 0，与人工锁定（记录真实用户 ID）区分开。
func (s *RSSService) autoLockMagnets(now time.Time) error {
	cfg := config.Get()
	if cfg == nil || !cfg.RSS.PikPak.AutoLock {
		return nil
	}

	res := s.db.Model(&model.RSSEntry{}).
		Where("magnet_locked = FALSE").
		Where("magnet_url <> ''").
		Updates(map[string]any{
			"magnet_locked":    true,
			"magnet_locked_at": now,
			"magnet_locked_by": 0,
		})
	if res.Error != nil {
		return fmt.Errorf("自动锁定磁力链接失败: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		logger.Info("[rss] 已自动锁定磁力链接", "count", res.RowsAffected)
	}
	return nil
}
