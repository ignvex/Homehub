package model

// User 系统用户模型。
//
// 单用户场景，仅用于认证登录。
// PasswordHash 通过 bcrypt 存储密码哈希，JSON 序列化时忽略（json:"-"）。
type User struct {
	BaseModel
	Username     string `gorm:"uniqueIndex;size:100;not null" json:"username"`
	PasswordHash string `gorm:"size:255;not null" json:"-"`
}

// TableName 指定表名为 users。
func (User) TableName() string {
	return "users"
}
