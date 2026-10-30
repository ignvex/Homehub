package model

import "time"

// ============================================================
// RSSImage — RSS 条目内嵌图片缓存
// ============================================================

// RSSImage 记录一张已缓存的 RSS 图片。
//
// 身份是**内容**而不是来源：CacheKey 取内容 SHA256，同一张图即使以不同
// URL 出现（CDN 签名、转载、时效 token）也只存一份文件。
// “哪个 URL 提供过它”由 RSSImageSource 记录，“谁引用过它”由
// RSSImageRef 记录。
//
// 文件名格式：`rss_<时间戳>_<随机>_<内容哈希前8>.<ext>`。时间与随机两段
// 是为了让文件名不可枚举、不随时间重复；唯一性则由 CacheKey 保证，
// 不依赖文件名。
//
// 表名：rss_images
type RSSImage struct {
	BaseModel
	CacheKey    string    `gorm:"size:64;uniqueIndex;not null" json:"cache_key"` // 内容 SHA256 的十六进制（旧数据可能是 md5(URL)）
	SourceURL   string    `gorm:"type:text;not null" json:"source_url"`          // 首次见到该内容时的源 URL（完整 URL 列表见 RSSImageSource）
	Filename    string    `gorm:"size:255;not null" json:"filename"`             // 缓存目录内的文件名（时间戳_随机_哈希）
	ContentType string    `gorm:"size:100" json:"content_type,omitempty"`        // 响应 Content-Type（或按扩展名推断）
	SizeBytes   int64     `gorm:"not null;default:0" json:"size_bytes"`          // 文件大小（字节）
	LastSeenAt  time.Time `gorm:"index;not null" json:"last_seen_at"`            // 最近一次被条目引用的时间
}

// TableName 指定表名。
func (RSSImage) TableName() string {
	return "rss_images"
}

// ============================================================
// RSSImageRef — 图片与订阅源/条目的引用关系
// ============================================================

// RSSImageRef 记录"某个条目引用了某张图片"。
//
// 条目本身已经是跨源共享的内容（见 RSSEntryFeed），所以引用只需要
// 指向条目：多个源收录同一篇文章时共用同一份引用，删除其中某个源
// 不会影响图片。
//
// 清理顺序：条目成为孤儿（不再被任何源收录）→ 删除其引用 →
// 没有任何引用的 RSSImage 与物理文件被回收。
//
// 表名：rss_image_refs
type RSSImageRef struct {
	BaseModel
	ImageID uint64 `gorm:"not null;uniqueIndex:idx_rss_image_ref" json:"image_id"`       // 图片 ID
	EntryID uint64 `gorm:"not null;index;uniqueIndex:idx_rss_image_ref" json:"entry_id"` // 条目 ID
}

// TableName 指定表名。
func (RSSImageRef) TableName() string {
	return "rss_image_refs"
}
