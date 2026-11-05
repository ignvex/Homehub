package model

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// User 系统用户模型。
//
// 权限由 Level 表达（1 级最高，见 permission.go）。Role 不是数据库列，
// 而是 Level 的派生值，只出现在 JSON 输出里 —— 见下面的 MarshalJSON。
// 这样就不会出现“role 说是 admin、level 说是 5”的不一致状态。
//
// PasswordHash 通过 bcrypt 存储密码哈希，JSON 序列化时忽略（json:"-"）。
// Email 为可选字段，用于 Gravatar 头像和 email 登录。
type User struct {
	BaseModel
	Username     string `gorm:"uniqueIndex;size:100;not null" json:"username"`
	PasswordHash string `gorm:"size:255;not null" json:"-"`
	Email        string `gorm:"size:255;default:''" json:"email,omitempty"`
	// Level 权限等级：1=admin 最高，5=只读。默认给最低权限，
	// 任何“忘了设等级”的路径都不会意外造出高权限账号。
	Level    int  `gorm:"not null;default:5;index" json:"level"`
	IsActive bool `gorm:"default:true" json:"is_active"` // 是否启用
}

// MarshalJSON 在输出时补上由 Level 派生的 role 字段。
//
// 保留 role 是为了兼容既有前端（它按 "admin" / "user" 判断）：改造期间
// 前端不需要同步改。level 与 role 永远不会矛盾，因为 role 不存储。
func (u User) MarshalJSON() ([]byte, error) {
	type Alias User
	return json.Marshal(struct {
		Alias
		Role string `json:"role"`
	}{
		Alias: Alias(u),
		Role:  LevelName(u.Level),
	})
}

// UserBrief 是对外展示用的最小用户信息（指派选择器、待办的创建者/被指派者）。
//
// 只有 id 与用户名：邮箱、等级都不该随一条待办泄露给所有登录用户。
type UserBrief struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

// TableName 指定表名为 users。
func (User) TableName() string {
	return "users"
}

// HasLevel 判断用户是否达到某个权限门槛。
//
// 语义是“至少这么高”：判 LevelAdmin 只放行 1 级，判 LevelEditor
// 放行 1–3 级。数值小 = 权限高。
//
// 先校验 Level 自身合法性：0、负数、6 都是无效值，一律拒绝。
// 少了这一步就是个 fail-open —— `Level=0` 满足 `0 <= 1`，
// 一个从没被正确初始化过的账号反而拿到最高权限。
func (u *User) HasLevel(required int) bool {
	if u == nil || !IsValidLevel(u.Level) {
		return false
	}
	return u.Level <= required
}

// IsAdmin 是 HasLevel(LevelAdmin) 的语义化快捷方式。
func (u *User) IsAdmin() bool {
	return u.HasLevel(LevelAdmin)
}

// CanManage 判断当前用户能否管理 target 这个账号。
//
// 规则：等级必须严格高于对方（数值更小），且不能动自己。
// 这样 2 级可以管 3–5 级，但碰不到同为 2 级或更高的账号 —— 同级之间
// 不能互相停用或改权限，避免权力互斗。
func (u *User) CanManage(target *User) bool {
	if u == nil || target == nil {
		return false
	}
	if u.ID == target.ID {
		return false
	}
	return u.Level < target.Level
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
