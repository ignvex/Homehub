package model

import "time"

// ============================================================
// RSSFeed — RSS 订阅源
// ============================================================

// RSSFeed RSS 订阅源模型。
//
// 每个订阅源对应一个 RSS/Atom feed URL，定时抓取条目。可归属到分组。
// 表名：rss_feeds
type RSSFeed struct {
	BaseModel
	Title         string    `gorm:"size:255;not null" json:"title"`            // 订阅源标题（抓取时自动更新）
	URL           string    `gorm:"uniqueIndex;size:2048;not null" json:"url"` // 订阅源 URL（唯一）
	SiteURL       string    `gorm:"size:2048" json:"site_url"`                 // 站点主页 URL
	Description   string    `gorm:"type:text" json:"description,omitempty"`    // 订阅源描述
	IconURL       string    `gorm:"size:2048" json:"icon_url,omitempty"`       // 站点图标 URL
	FetchInterval int       `gorm:"default:3600" json:"fetch_interval"`        // 抓取间隔（秒），默认 3600
	LastFetch     time.Time `json:"last_fetch"`                                // 最近一次抓取时间
	ErrorMsg      string    `gorm:"size:1024" json:"error_msg,omitempty"`      // 最近一次错误信息
	GroupID       *uint64   `gorm:"index" json:"group_id,omitempty"`           // 所属分组 ID（NULL 表示未分组）
}

// TableName 指定表名。
func (RSSFeed) TableName() string {
	return "rss_feeds"
}

// ============================================================
// RSSEntry — RSS 条目
// ============================================================

// RSSEntry RSS 条目模型（纯内容，不归属任何源）。
//
// 按 link 全局去重：同一篇文章被多个 feed 收录时只有一行，谁收录了它
// 由 RSSEntryFeed 记录。这样删除一个源不会带走其他源仍在引用的内容，
// 正文也不必按源重复存储。
//
// Published/FetchedAt 是"首次见到"的时间；每个源自己的发布时间在
// RSSEntryFeed.Published 上。
//
// 表名：rss_entries
type RSSEntry struct {
	BaseModel
	Title     string    `gorm:"size:500;not null" json:"title"`             // 条目标题
	Link      string    `gorm:"uniqueIndex;size:2048;not null" json:"link"` // 条目链接（全表唯一）
	Author    string    `gorm:"size:255" json:"author,omitempty"`           // 作者
	Content   string    `gorm:"type:text" json:"content,omitempty"`         // 清洗后的正文
	MagnetURL string    `gorm:"type:text" json:"magnet_url,omitempty"`      // 磁力链接
	Published time.Time `gorm:"index" json:"published"`                     // 首次见到的发布时间
	FetchedAt time.Time `gorm:"not null" json:"fetched_at"`                 // 首次抓取时间

	// ---- 磁力链接锁定与 PikPak 提交状态 ----
	//
	// 锁定的含义：这条磁力链接已被人工确认，后续抓取不得改写它。
	// 抓取时的 ON CONFLICT 已经有“空值不覆盖”的保护，但那只挡得住
	// 空值；源站换了磁力链接时仍会覆盖。锁定就是把这个口子关上。
	//
	// 锁定与 PikPak 是两件独立的事：可以只锁定不提交，也可以在未启用的
	// 情况下单纯标记。所以两者字段分开，状态也分开。
	MagnetLocked   bool       `gorm:"not null;default:false;index" json:"magnet_locked"`
	MagnetLockedAt *time.Time `json:"magnet_locked_at,omitempty"`
	MagnetLockedBy uint64     `gorm:"not null;default:0" json:"magnet_locked_by,omitempty"`

	// PikPakStatus 提交状态：空 / pending / saved / failed。
	// 用短字符串而不是枚举类型：改状态不需要 DDL。
	//
	// 这四列都显式指定 column：GORM 会把 PikPak 拆成 pik_pak，
	// 得到 pik_pak_status 这种与 JSON、文档、手写 SQL 都不一致的名字。
	// 别名一旦分叉，排查时最容易在这里踩坑。
	PikPakStatus  string     `gorm:"column:pikpak_status;size:20;not null;default:'';index" json:"pikpak_status,omitempty"`
	PikPakTaskID  string     `gorm:"column:pikpak_task_id;size:128" json:"pikpak_task_id,omitempty"`
	PikPakError   string     `gorm:"column:pikpak_error;size:512" json:"pikpak_error,omitempty"`
	PikPakSavedAt *time.Time `gorm:"column:pikpak_saved_at" json:"pikpak_saved_at,omitempty"`
}

// TableName 指定表名。
func (RSSEntry) TableName() string {
	return "rss_entries"
}

// ============================================================
// RSSEntryFeed — 条目与订阅源的收录关系
// ============================================================

// RSSEntryFeed 记录"某个订阅源收录了某个条目"。
//
// 这是条目归属的唯一来源：删除订阅源只删这里的行，只有当一个条目
// 不再被任何源收录时才回收内容本身。Published 是**该源**给出的发布
// 时间，不同源可能不一致，按源筛选时按它排序。
//
// 表名：rss_entry_feeds
type RSSEntryFeed struct {
	BaseModel
	EntryID     uint64    `gorm:"not null;uniqueIndex:idx_rss_entry_feed" json:"entry_id"`      // 条目 ID
	FeedID      uint64    `gorm:"not null;index;uniqueIndex:idx_rss_entry_feed" json:"feed_id"` // 订阅源 ID
	Published   time.Time `gorm:"index" json:"published"`                                       // 该源给出的发布时间
	FirstSeenAt time.Time `gorm:"not null" json:"first_seen_at"`                                // 首次被该源收录
	LastSeenAt  time.Time `gorm:"not null" json:"last_seen_at"`                                 // 最近一次出现在该源
}

// TableName 指定表名。
func (RSSEntryFeed) TableName() string {
	return "rss_entry_feeds"
}
