package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
)

// Paste 剪贴板模型。
type Paste struct {
	BaseModel
	PublicID    string    `gorm:"uniqueIndex;size:12;not null" json:"public_id"`
	Content     string    `gorm:"type:text;not null" json:"content"`
	Language    string    `gorm:"size:50" json:"language,omitempty"`
	DeleteToken string    `gorm:"size:64;not null" json:"-"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

// TableName 指定表名为 homehub_pastes。
func (Paste) TableName() string {
	return "homehub_pastes"
}

// CreatePastesTable 创建 pastes 表（独立于 AutoMigrate）。
func CreatePastesTable(db *gorm.DB) error {
	return db.Exec(`
		CREATE TABLE IF NOT EXISTS homehub_pastes (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			public_id VARCHAR(12) NOT NULL UNIQUE,
			content TEXT NOT NULL,
			language VARCHAR(50) DEFAULT '',
			delete_token VARCHAR(64) NOT NULL,
			expires_at TIMESTAMPTZ
		)
	`).Error
}

// NanoID 生成指定长度的随机字母数字 ID。
func NanoID(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	buf := make([]byte, 1)
	for i := range result {
		_, _ = rand.Read(buf)
		result[i] = chars[int(buf[0])%len(chars)]
	}
	return string(result)
}

// RandomToken 生成 n 字节的随机十六进制 token。
func RandomToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
