package model

import "time"

// ============================================================
// Todo — 待办事项
// ============================================================

// Todo 待办事项模型。
//
// 用户可创建个人待办事项，支持多种状态和优先级排序。
// 表名：todos
type Todo struct {
	BaseModel
	Title       string     `gorm:"size:500;not null" json:"title"`                 // 任务标题
	Description string     `gorm:"type:text" json:"description,omitempty"`          // 详细描述
	Status      string     `gorm:"size:20;not null;default:'pending';index" json:"status"` // 状态：planned/pending/in_progress/completed/paused
	Priority    int        `gorm:"default:0" json:"priority"`                       // 优先级，值越大越靠前
	DueDate     *time.Time `json:"due_date,omitempty"`                              // 截止日期（可选）
	SortOrder   int        `gorm:"default:0" json:"sort_order"`                     // 拖拽排序
}

// TableName 指定表名。
func (Todo) TableName() string {
	return "todos"
}

// TodoStatuses 返回所有合法状态值列表。
func TodoStatuses() []string {
	return []string{"planned", "pending", "in_progress", "completed", "paused"}
}

// IsValidTodoStatus 校验状态值是否合法。
func IsValidTodoStatus(s string) bool {
	for _, valid := range TodoStatuses() {
		if s == valid {
			return true
		}
	}
	return false
}
