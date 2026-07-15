package model

import "time"

// Session 用户会话模型。
type Session struct {
	BaseModel
	UserID    uint64    `gorm:"index;not null" json:"user_id"`
	Token     string    `gorm:"uniqueIndex;size:255;not null" json:"token"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
}

// TableName 指定表名为 homehub_sessions。
func (Session) TableName() string {
	return "homehub_sessions"
}
