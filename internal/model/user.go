package model

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// User 系统用户模型。
//
// 支持多用户场景，包含角色（admin / user）和启用状态。
// PasswordHash 通过 bcrypt 存储密码哈希，JSON 序列化时忽略（json:"-"）。
// Email 为可选字段，用于 Gravatar 头像和 email 登录。
type User struct {
	BaseModel
	Username     string `gorm:"uniqueIndex;size:100;not null" json:"username"`
	PasswordHash string `gorm:"size:255;not null" json:"-"`
	Email        string `gorm:"size:255;default:''" json:"email,omitempty"`
	Role         string `gorm:"size:20;not null;default:'user'" json:"role"` // 角色：admin / user
	IsActive     bool   `gorm:"default:true" json:"is_active"`               // 是否启用
}

// TableName 指定表名为 users。
func (User) TableName() string {
	return "homehub_users"
}

// GravatarHash 返回邮箱的小写去空格后 MD5 哈希（十六进制字符串）。
//
// 若 email 为空字符串，返回空字符串。
// 此方法是生成 Gravatar URL 的前置步骤。
func (u *User) GravatarHash() string {
	email := strings.TrimSpace(strings.ToLower(u.Email))
	if email == "" {
		return ""
	}
	hash := md5.Sum([]byte(email))
	return hex.EncodeToString(hash[:])
}

// GravatarURL 根据给定参数生成 Gravatar 头像的外部 URL。
//
// 参数：
//   - size：头像尺寸（像素）
//   - defaultStyle：无头像时的默认风格（identicon、mp、robohash 等）
//   - rating：内容分级（g、pg、r、x）
//
// 若 email 为空，返回空字符串。
func (u *User) GravatarURL(size int, defaultStyle, rating string) string {
	hash := u.GravatarHash()
	if hash == "" {
		return ""
	}
	return fmt.Sprintf("https://www.gravatar.com/avatar/%s?s=%d&d=%s&r=%s",
		hash, size, defaultStyle, rating)
}
