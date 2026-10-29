package model

// AvatarCache Gravatar 头像缓存模型。
//
// 将从 Gravatar 下载的头像图片二进制数据存储到数据库，避免每次请求都访问外部服务。
// 每个用户仅保留一份缓存（UserID 唯一约束），通过 ETag 和 LastModified 支持增量更新检测。
type AvatarCache struct {
	BaseModel
	UserID       uint64 `gorm:"uniqueIndex;not null" json:"user_id"`  // 关联的用户 ID
	EmailHash    string `gorm:"size:64;not null" json:"email_hash"`   // 邮箱的 MD5 十六进制哈希
	ImageData    []byte `gorm:"type:bytea;not null" json:"-"`         // 头像图片二进制数据
	ContentType  string `gorm:"size:50;not null" json:"content_type"` // MIME 类型（如 image/jpeg）
	ETag         string `gorm:"size:255" json:"etag"`                 // Gravatar 返回的 ETag（用于 304 检测）
	LastModified string `gorm:"size:100" json:"last_modified"`        // Gravatar 返回的 Last-Modified
}

// TableName 指定表名为 avatar_caches。
func (AvatarCache) TableName() string {
	return "homehub_avatar_caches"
}
