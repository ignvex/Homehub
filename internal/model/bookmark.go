package model

// BookmarkGroup 书签分组模型。
type BookmarkGroup struct {
	BaseModel
	Name      string     `gorm:"size:100;not null" json:"name"`
	SortOrder int        `gorm:"default:0" json:"sort_order"`
	Priority  int        `gorm:"default:0" json:"priority"`
	Bookmarks []Bookmark `gorm:"foreignKey:GroupID" json:"bookmarks,omitempty"`
}

// TableName 指定表名为 bookmark_groups。
func (BookmarkGroup) TableName() string {
	return "bookmark_groups"
}

// Bookmark 书签模型。
type Bookmark struct {
	BaseModel
	GroupID   uint64 `gorm:"index;not null" json:"group_id"`
	Title     string `gorm:"size:255;not null" json:"title"`
	URL       string `gorm:"size:2048;not null" json:"url"`
	Icon      string `gorm:"size:2048" json:"icon,omitempty"`
	SortOrder int    `gorm:"default:0" json:"sort_order"`
	Priority  int    `gorm:"default:0" json:"priority"`
}

// TableName 指定表名为 bookmarks。
func (Bookmark) TableName() string {
	return "bookmarks"
}
