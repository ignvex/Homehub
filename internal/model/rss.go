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
	return "homehub_rss_feeds"
}

// ============================================================
// RSSEntry — RSS 条目
// ============================================================

// RSSEntry RSS 条目模型。
//
// 每个条目属于一个订阅源，按 link 去重。
// 表名：rss_entries
type RSSEntry struct {
	BaseModel
	FeedID    uint64    `gorm:"index;not null" json:"feed_id"`              // 所属订阅源 ID
	Title     string    `gorm:"size:500;not null" json:"title"`             // 条目标题
	Link      string    `gorm:"uniqueIndex;size:2048;not null" json:"link"` // 条目链接（唯一，用于去重）
	Author    string    `gorm:"size:255" json:"author,omitempty"`           // 作者
	Content   string    `gorm:"type:text" json:"content,omitempty"`         // 摘要/正文（截断到 2000 字符）
	MagnetURL string    `gorm:"type:text" json:"magnet_url,omitempty"`      // 磁力链接（从 RSS 附件或内容中提取）
	Published time.Time `gorm:"index" json:"published"`                     // 发布时间
	FetchedAt time.Time `gorm:"not null" json:"fetched_at"`                 // 抓取时间
}

// TableName 指定表名。
func (RSSEntry) TableName() string {
	return "homehub_rss_entries"
}
