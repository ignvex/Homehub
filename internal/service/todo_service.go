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
	if err := s.db.Model(&model.Todo{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
		return nil, fmt.Errorf("查询待办排序失败: %w", err)
	}

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

// Optional 表达三态更新：Set=false 不改；Set=true 且 Value=nil 清空；否则写入 *Value。
//
// 与 RSS 的 GroupUpdate 同一个理由：只用指针分不清「没传」与「传了 null」。
type Optional[T any] struct {
	Set   bool
	Value *T
}

// TodoUpdate 表达对一条待办的全部更新意图。nil / Set=false 表示不改。
type TodoUpdate struct {
	Title    *string
	Status   *string
	Priority *int
	// Description 清空时写入空串（列是 text，旧数据也都是空串而非 NULL）。
	Description Optional[string]
	// DueDate 清空时写入 NULL。
	DueDate Optional[time.Time]
}

// buildTodoUpdates 把更新意图翻译成 SET 子句。
func buildTodoUpdates(u TodoUpdate) (map[string]interface{}, error) {
	updates := map[string]interface{}{}

	if u.Title != nil {
		title := strings.TrimSpace(*u.Title)
		// 标题是必填列：传空串视为「不改」，而不是写一个空标题。
		if title != "" {
			updates["title"] = model.TruncateRunes(title, 500)
		}
	}
	if u.Description.Set {
		if u.Description.Value == nil {
			updates["description"] = ""
		} else {
			updates["description"] = *u.Description.Value
		}
	}
	if u.Status != nil {
		status := strings.TrimSpace(*u.Status)
		if status != "" {
			if !model.IsValidTodoStatus(status) {
				return nil, fmt.Errorf("%w: %s", ErrTodoBadStatus, status)
			}
			updates["status"] = status
		}
	}
	if u.Priority != nil {
		updates["priority"] = *u.Priority
	}
	if u.DueDate.Set {
		if u.DueDate.Value == nil {
			updates["due_date"] = nil
		} else {
			updates["due_date"] = *u.DueDate.Value
		}
	}
	return updates, nil
}

// UpdateTodo 部分更新待办事项。
func (s *TodoService) UpdateTodo(id uint64, u TodoUpdate) error {
	updates, err := buildTodoUpdates(u)
	if err != nil {
		return err
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
// ids 为待办 ID 列表，按期望顺序排列。一条 UPDATE 完成，整体生效或整体不变。
func (s *TodoService) ReorderTodos(ids []uint64) error {
	if err := reorderBySortOrder(s.db, "todos", ids, ""); err != nil {
		return fmt.Errorf("更新待办排序失败: %w", err)
	}
	return nil
}

// ============================================================
// 业务错误
// ============================================================

var (
	ErrTodoNotFound = errors.New("待办事项不存在")
	// ErrTodoBadStatus 表示状态值不在白名单内，handler 据此回 400。
	ErrTodoBadStatus = errors.New("无效的待办状态")
)
