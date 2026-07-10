package model

import "time"

// Session 用户会话模型。
//
// 记录用户登录后生成的 session token，
// 有过期时间，用于认证中间件校验。
type Session struct {
	BaseModel
	UserID    uint64    `gorm:"index;not null" json:"user_id"`
	Token     string    `gorm:"uniqueIndex;size:255;not null" json:"token"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
}

// TableName 指定表名为 sessions。
func (Session) TableName() string {
	return "sessions"
}
