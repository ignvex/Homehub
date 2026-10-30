package model

import "time"

// ============================================================
// RSSImage — RSS 条目内嵌图片缓存
// ============================================================

// RSSImage 记录一张已缓存的 RSS 图片。
//
// 物理文件按内容源 URL 寻址（`rss_<md5(url)>.<ext>`），同一 URL 在多个
// 订阅源之间共享同一份文件；"谁引用过它"由 RSSImageRef 记录。
// 这样既避免重复下载，又能回答"A 源删除时这张图能不能删"。
//
// 表名：homehub_rss_images
type RSSImage struct {
	BaseModel
	CacheKey    string    `gorm:"size:64;uniqueIndex;not null" json:"cache_key"` // 缓存键：md5(源 URL) 的十六进制
	SourceURL   string    `gorm:"type:text;not null" json:"source_url"`          // 原始图片 URL（可能带很长的签名参数）
	Filename    string    `gorm:"size:255;not null" json:"filename"`             // 缓存目录内的文件名
	ContentType string    `gorm:"size:100" json:"content_type,omitempty"`        // 响应 Content-Type（或按扩展名推断）
	SizeBytes   int64     `gorm:"not null;default:0" json:"size_bytes"`          // 文件大小（字节）
	SHA256      string    `gorm:"size:64" json:"sha256,omitempty"`               // 内容哈希；历史文件可能为空
	LastSeenAt  time.Time `gorm:"index;not null" json:"last_seen_at"`            // 最近一次被条目引用的时间
}

// TableName 指定表名。
func (RSSImage) TableName() string {
	return "homehub_rss_images"
}

// ============================================================
// RSSImageRef — 图片与订阅源/条目的引用关系
// ============================================================

// RSSImageRef 记录"某个订阅源的某个条目引用了某张图片"。
//
// 同一张图可以被多个源、多个条目引用；清理时必须先删引用，再删
// 没有任何引用的 RSSImage 与物理文件。EntryHash 是条目链接的
// sha256，避免 2048 字符链接直接把联合唯一索引撑爆。
//
// 表名：homehub_rss_image_refs
type RSSImageRef struct {
	BaseModel
	ImageID   uint64 `gorm:"not null;uniqueIndex:idx_rss_image_ref" json:"image_id"`           // 图片 ID
	FeedID    uint64 `gorm:"not null;index;uniqueIndex:idx_rss_image_ref" json:"feed_id"`      // 订阅源 ID
	EntryHash string `gorm:"size:64;not null;uniqueIndex:idx_rss_image_ref" json:"entry_hash"` // sha256(条目 link)
}

// TableName 指定表名。
func (RSSImageRef) TableName() string {
	return "homehub_rss_image_refs"
}
