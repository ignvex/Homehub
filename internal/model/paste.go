package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
)

// Paste 剪贴板模型。
//
// 刻意不包含语言标记等未被使用的字段：这些字段从未被写入，
// 留着只会让建表 DDL 与模型产生误导。
type Paste struct {
	BaseModel
	PublicID    string    `gorm:"uniqueIndex;size:12;not null" json:"public_id"`
	Content     string    `gorm:"type:text;not null" json:"content"`
	DeleteToken string    `gorm:"size:64;not null" json:"-"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

// TableName 指定表名为 pastes。
func (Paste) TableName() string {
	return "pastes"
}

// CreatePastesTable 创建 pastes 表与 expires_at 索引（独立于 AutoMigrate）。
//
// 列表与过期清理都按 expires_at 过滤，没有索引时每次清理都是全表扫描。
func CreatePastesTable(db *gorm.DB) error {
	if err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pastes (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			public_id VARCHAR(12) NOT NULL UNIQUE,
			content TEXT NOT NULL,
			delete_token VARCHAR(64) NOT NULL,
			expires_at TIMESTAMPTZ
		)
	`).Error; err != nil {
		return err
	}
	return db.Exec(`CREATE INDEX IF NOT EXISTS idx_pastes_expires_at ON pastes (expires_at)`).Error
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
