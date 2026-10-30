package model

// RSSImageSource 记录"某个源 URL 提供过这张图片"。
//
// 为什么需要它：rss_images 按**内容哈希**去重，同一张图片即使以
// 不同 URL 出现（CDN 签名参数、转载站改域名、时效 token）也只存一份文件。
// 但抓取时拿到的是 URL，必须先知道它对应哪份内容才能避免重复下载——
// 这就是这张映射表的作用：URL → 图片。
//
// 一对多关系：一张图片可以有多行（多个 URL 指向同一内容），反向则只有
// 一行（一个 URL 只对应一份内容）。
//
// 为什么不把 URL 直接塞进 rss_images：一个 content 对多个 URL，
// 存成 JSON 数组既无法建唯一索引，也无法用 WHERE url = ? 快速查找；
// 而抓取热路径恰恰需要这个查找。
//
// 表名：rss_image_sources
type RSSImageSource struct {
	BaseModel
	ImageID   uint64 `gorm:"not null;index" json:"image_id"`              // 指向 rss_images.id
	URLKey    string `gorm:"size:64;uniqueIndex;not null" json:"url_key"` // sha256(URL) 的十六进制，用于快速反查
	SourceURL string `gorm:"type:text;not null" json:"source_url"`        // 原始 URL（可能带很长的签名参数）
}

// TableName 指定表名。
func (RSSImageSource) TableName() string {
	return "rss_image_sources"
}
