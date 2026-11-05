package model

import "time"

// RSSEntryState 记录某个用户对某个条目的阅读状态（已读、星标）。
//
// 阅读状态是个人数据：同一条目对不同用户可以分别是已读与未读。
// 主键是 (user_id, entry_id)，所以「一个用户对一个条目」只有一行，
// 列表查询 LEFT JOIN 时不会把条目复制成多行。
//
// 不嵌入 BaseModel：这张表没有自增 id 的用途，也不需要软删除——
// 软删除行会占住联合主键，取消已读后再标记就会撞键。
//
// 没有数据库外键（全库约定，见 docs/tables.md §1.3），清理在应用层：
//   - 条目成为孤儿被物理删除时（删订阅源、保留期清理），
//     RSSService.purgeOrphanEntries 一并删除对应行
//   - 用户被删除时，AuthService.DeleteUser 在同一事务里删除该用户的行
//
// 表名：rss_entry_states
type RSSEntryState struct {
	UserID  uint64 `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	EntryID uint64 `gorm:"primaryKey;autoIncrement:false;index" json:"entry_id"`
	// ReadAt 非 NULL 表示已读。用时间而不是布尔值：将来做「最近读过」不必改表。
	ReadAt *time.Time `json:"read_at,omitempty"`
	// StarredAt 非 NULL 表示已加星标。
	StarredAt *time.Time `json:"starred_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (RSSEntryState) TableName() string {
	return "rss_entry_states"
}
