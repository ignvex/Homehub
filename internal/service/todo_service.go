package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// ============================================================
// TodoService — 待办事项业务服务
// ============================================================

// TodoService 提供待办事项的 CRUD 和排序功能。
type TodoService struct {
	db *gorm.DB
}

// NewTodoService 创建 TodoService 实例。
func NewTodoService(db *gorm.DB) *TodoService {
	return &TodoService{db: db}
}

// ============================================================
// 查询
// ============================================================

// ListTodos 返回待办事项列表，支持按状态筛选和排序。
//
// 参数：
//   - status: 按状态筛选，空字符串表示返回全部
//   - sort: 排序方式，支持 "priority"（默认，priority DESC, sort_order ASC）、
//     "due_date"（due_date ASC）或 "created_at"（created_at DESC）
func (s *TodoService) ListTodos(status string, sort string) ([]model.Todo, error) {
	query := s.db.Model(&model.Todo{})

	// 状态筛选
	status = strings.TrimSpace(status)
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 排序
	sort = strings.TrimSpace(sort)
	switch sort {
	case "due_date":
		query = query.Order("due_date ASC")
	case "created_at":
		query = query.Order("created_at DESC")
	default:
		query = query.Order("priority DESC, sort_order ASC")
	}

	var todos []model.Todo
	if err := query.Find(&todos).Error; err != nil {
		return nil, fmt.Errorf("查询待办列表失败: %w", err)
	}

	return todos, nil
}

// GetTodo 根据 ID 获取待办事项详情。
func (s *TodoService) GetTodo(id uint64) (*model.Todo, error) {
	var todo model.Todo
	if err := s.db.First(&todo, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTodoNotFound
		}
		return nil, fmt.Errorf("查询待办事项失败: %w", err)
	}
	return &todo, nil
}

// ============================================================
// 创建
// ============================================================

// CreateTodo 创建新的待办事项。
//
// priority 为优先级（值越大越靠前），dueDate 为可选截止日期。
func (s *TodoService) CreateTodo(title, description string, priority int, dueDate *time.Time) (*model.Todo, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("待办标题不能为空")
	}

	// 获取当前最大排序值
	var maxOrder int
	s.db.Model(&model.Todo{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)

	todo := model.Todo{
		Title:       title,
		Description: description,
		Status:      "pending",
		Priority:    priority,
		DueDate:     dueDate,
		SortOrder:   maxOrder + 1,
	}

	if err := s.db.Create(&todo).Error; err != nil {
		return nil, fmt.Errorf("创建待办事项失败: %w", err)
	}

	return &todo, nil
}

// ============================================================
// 更新
// ============================================================

// UpdateTodo 部分更新待办事项。
//
// 所有字段均为可选：非零值/非 nil 的字段才会被更新。
//   - title, description, status: 空字符串表示不更新
//   - priority: nil 表示不更新
//   - dueDate: nil 表示不更新
func (s *TodoService) UpdateTodo(id uint64, title, description, status string, priority *int, dueDate *time.Time) error {
	updates := map[string]interface{}{}

	title = strings.TrimSpace(title)
	if title != "" {
		updates["title"] = title
	}
	if description != "" {
		updates["description"] = description
	}

	status = strings.TrimSpace(status)
	if status != "" {
		if !model.IsValidTodoStatus(status) {
			return fmt.Errorf("无效的待办状态: %s", status)
		}
		updates["status"] = status
	}

	if priority != nil {
		updates["priority"] = *priority
	}
	if dueDate != nil {
		updates["due_date"] = *dueDate
	}

	if len(updates) == 0 {
		return nil
	}

	result := s.db.Model(&model.Todo{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("更新待办事项失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrTodoNotFound
	}

	return nil
}

// ============================================================
// 删除
// ============================================================

// DeleteTodo 根据 ID 删除待办事项。
func (s *TodoService) DeleteTodo(id uint64) error {
	result := s.db.Delete(&model.Todo{}, id)
	if result.Error != nil {
		return fmt.Errorf("删除待办事项失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrTodoNotFound
	}
	return nil
}

// ============================================================
// 排序
// ============================================================

// ReorderTodos 按传入的 ID 顺序更新待办的 sort_order。
//
// ids 为待办 ID 列表，按期望顺序排列。
func (s *TodoService) ReorderTodos(ids []uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.Todo{}).Where("id = ?", id).
				Update("sort_order", i+1).Error; err != nil {
				return fmt.Errorf("更新待办排序失败: %w", err)
			}
		}
		return nil
	})
}

// ============================================================
// 业务错误
// ============================================================

var ErrTodoNotFound = errors.New("待办事项不存在")
