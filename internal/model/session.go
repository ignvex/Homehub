package model

import "time"

// Session 用户会话模型。
type Session struct {
	BaseModel
	UserID uint64 `gorm:"index;not null" json:"user_id"`
	Token  string `gorm:"uniqueIndex;size:255;not null" json:"token"`
	// ExpiresAt 带索引：过期清理是 `WHERE expires_at < now()`，
	// 没有索引时每次清理都是全表扫描，表越大越慢。
	ExpiresAt time.Time `gorm:"index;not null" json:"expires_at"`
}

// TableName 指定表名为 sessions。
func (Session) TableName() string {
	return "sessions"
}
