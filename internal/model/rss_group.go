package model

// ============================================================
// RSSGroup — RSS 订阅分组
// ============================================================

// RSSGroup RSS 订阅分组模型。
//
// 用于对 RSS 订阅源进行分类管理。
// 表名：rss_groups
type RSSGroup struct {
	BaseModel
	Name      string    `gorm:"size:100;not null" json:"name"`             // 分组名称
	SortOrder int       `gorm:"default:0" json:"sort_order"`               // 排序序号
	Feeds     []RSSFeed `gorm:"foreignKey:GroupID" json:"feeds,omitempty"` // 关联的订阅源
}

// TableName 指定表名。
func (RSSGroup) TableName() string {
	return "homehub_rss_groups"
}
