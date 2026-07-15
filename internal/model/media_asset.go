package model

// MediaAsset 媒体资源模型。
//
// 存储用户上传的图片和视频文件元信息。
// 物理文件保存在 web/static/uploads/ 目录下，
// 数据库仅记录元数据用于 API 查询和关联。
type MediaAsset struct {
	BaseModel
	Filename     string `gorm:"size:255;not null" json:"filename"`        // 存储文件名（UUID）
	OriginalName string `gorm:"size:255;not null" json:"original_name"`   // 原始文件名
	FilePath     string `gorm:"size:512;not null" json:"file_path"`       // 相对路径（如 uploads/xxx.jpg）
	FileSize     int64  `gorm:"not null" json:"file_size"`                // 文件大小（字节）
	MimeType     string `gorm:"size:100;not null" json:"mime_type"`       // MIME 类型
	MediaType    string `gorm:"size:20;not null;index" json:"media_type"` // 媒体类型：image / video
	Width        int    `gorm:"default:0" json:"width,omitempty"`         // 图片宽度（像素）
	Height       int    `gorm:"default:0" json:"height,omitempty"`        // 图片高度（像素）
}

// TableName 指定表名为 media_assets。
func (MediaAsset) TableName() string {
	return "homehub_media_assets"
}
