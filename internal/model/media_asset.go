package model

// MediaAsset 媒体资源模型。
//
// 只存**用户上传**的资产（图床主体），不包含 RSS 抓取的图片。
// 后者有自己的表 rss_images，因为它属于可回收的抓取缓存：
// 按内容去重、引用计数、按 last_seen_at 自动清理；而用户上传的资产
// 是长期持有的图床内容，生命周期完全不同，不能共用一张表。
//
// Source 字段用于区分来源。目前只有 upload（用户上传），预留 rss 以便
// 将来需要把抓取图片也展示在图床管理页时不必改表结构。
//
// 物理文件保存在 web/static/uploads/ 目录下，通过 /static/uploads/ 公开
// 读取（图床需要外部能直接访问）；数据库仅记录元数据用于 API 查询。
type MediaAsset struct {
	BaseModel
	Filename     string `gorm:"size:255;not null" json:"filename"`                     // 存储文件名（UUID）
	OriginalName string `gorm:"size:255;not null" json:"original_name"`                // 原始文件名
	FilePath     string `gorm:"size:512;not null" json:"file_path"`                    // 相对路径（如 uploads/xxx.jpg）
	FileSize     int64  `gorm:"not null" json:"file_size"`                             // 文件大小（字节）
	SHA256       string `gorm:"size:64;index" json:"sha256,omitempty"`                 // 内容哈希，用于去重与完整性校验
	MimeType     string `gorm:"size:100;not null" json:"mime_type"`                    // MIME 类型
	MediaType    string `gorm:"size:20;not null;index" json:"media_type"`              // 媒体类型：image / video / audio / document
	Source       string `gorm:"size:20;not null;default:'upload';index" json:"source"` // 来源：upload（用户上传）/ rss（抓取缓存）
	// OwnerID 是上传者。图床的语义是“我传的图归我管”，没有这一列时
	// 任何登录用户都能删掉别人的图（含正在当背景用的）。
	// 0 表示历史数据（升级前的记录没有归属信息），只有 1–3 级能动。
	OwnerID uint64 `gorm:"index;not null;default:0" json:"owner_id"`
	Width   int    `gorm:"default:0" json:"width,omitempty"`  // 图片宽度（像素）
	Height  int    `gorm:"default:0" json:"height,omitempty"` // 图片高度（像素）
}

// TableName 指定表名为 media_assets。
func (MediaAsset) TableName() string {
	return "media_assets"
}
