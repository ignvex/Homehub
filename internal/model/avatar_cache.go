package model

// AvatarCache 头像缓存模型。
//
// 将从上游（官方 Gravatar 或 extension）下载的头像二进制数据存储到数据库，
// 避免每次请求都访问外部服务。每个用户保留一份缓存（UserID 唯一约束），
// 所有显示位置共用同一张图，由浏览器缩放 —— 与 Gravatar 官方行为一致。
//
// Variant 记录这张图是用哪组参数下载的（来源、尺寸、风格、分级），
// 参数变化后旧图不再命中，避免改了配置却一直显示旧头像。
// EmailHash 用于判断邮箱是否变更，变更后同样触发重新下载。
type AvatarCache struct {
	BaseModel
	UserID uint64 `gorm:"uniqueIndex;not null" json:"user_id"` // 关联的用户 ID
	// Variant 是 "来源|尺寸|风格|分级" 形式的可解析指纹。
	Variant      string `gorm:"size:160;not null" json:"variant"`
	EmailHash    string `gorm:"size:64;not null" json:"email_hash"`   // 邮箱的 MD5 十六进制哈希
	ImageData    []byte `gorm:"type:bytea;not null" json:"-"`         // 头像图片二进制数据
	ContentType  string `gorm:"size:50;not null" json:"content_type"` // 已校验的 MIME 类型（image/jpeg 等）
	ETag         string `gorm:"size:255" json:"etag"`                 // 上游 ETag（用于 304 检测）
	LastModified string `gorm:"size:100" json:"last_modified"`        // 上游 Last-Modified
}

// TableName 指定表名为 avatar_caches。
func (AvatarCache) TableName() string {
	return "homehub_avatar_caches"
}
