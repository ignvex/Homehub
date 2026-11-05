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

// TodoFilter 是待办列表的查询条件。
type TodoFilter struct {
	// Status 为空表示不过滤。
	Status string
	// Sort 取 priority（默认）/ due_date / created_at。
	Sort string
	// AssigneeID 非 nil 时只返回指派给该用户的待办。
	AssigneeID *uint64
	// CreatedBy 非 nil 时只返回该用户创建的待办。
	CreatedBy *uint64
}

// ListTodos 返回待办事项列表，支持按状态、被指派者、创建者筛选和排序。
//
// 排序：
//   - "priority"（默认）：priority DESC, sort_order ASC
//   - "due_date"：due_date ASC
//   - "created_at"：created_at DESC
//
// 每条附带 creator / assignee 的 {id, username}，一次批量查询补齐。
func (s *TodoService) ListTodos(f TodoFilter) ([]model.Todo, error) {
	query := s.db.Model(&model.Todo{})

	if status := strings.TrimSpace(f.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if f.AssigneeID != nil {
		query = query.Where("assignee_id = ?", *f.AssigneeID)
	}
	if f.CreatedBy != nil {
		query = query.Where("created_by = ?", *f.CreatedBy)
	}

	switch strings.TrimSpace(f.Sort) {
	case "due_date":
		query = query.Order("due_date ASC")
	case "created_at":
		query = query.Order("created_at DESC")
	default:
		query = query.Order("priority DESC, sort_order ASC")
	}

	todos := []model.Todo{}
	if err := query.Find(&todos).Error; err != nil {
		return nil, fmt.Errorf("查询待办列表失败: %w", err)
	}
	if err := s.attachUsers(todos); err != nil {
		return nil, err
	}
	return todos, nil
}

// GetTodo 根据 ID 获取待办事项详情（附带 creator / assignee）。
func (s *TodoService) GetTodo(id uint64) (*model.Todo, error) {
	todo, err := s.loadTodo(id)
	if err != nil {
		return nil, err
	}
	list := []model.Todo{*todo}
	if err := s.attachUsers(list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// loadTodo 只取一行，不补用户信息。
func (s *TodoService) loadTodo(id uint64) (*model.Todo, error) {
	var todo model.Todo
	if err := s.db.First(&todo, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTodoNotFound
		}
		return nil, fmt.Errorf("查询待办事项失败: %w", err)
	}
	return &todo, nil
}

// attachUsers 批量补齐 creator / assignee，一条 IN 查询，不做 N+1。
//
// 用户已被删除（理论上 DeleteUser 会把引用置空）时对应字段留 nil。
func (s *TodoService) attachUsers(todos []model.Todo) error {
	idSet := map[uint64]struct{}{}
	for _, t := range todos {
		if t.CreatedBy != nil {
			idSet[*t.CreatedBy] = struct{}{}
		}
		if t.AssigneeID != nil {
			idSet[*t.AssigneeID] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var users []model.UserBrief
	if err := s.db.Model(&model.User{}).Select("id", "username").
		Where("id IN ?", ids).Scan(&users).Error; err != nil {
		return fmt.Errorf("查询待办关联用户失败: %w", err)
	}
	byID := make(map[uint64]*model.UserBrief, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}
	for i := range todos {
		if todos[i].CreatedBy != nil {
			todos[i].Creator = byID[*todos[i].CreatedBy]
		}
		if todos[i].AssigneeID != nil {
			todos[i].Assignee = byID[*todos[i].AssigneeID]
		}
	}
	return nil
}

// ============================================================
// 权限
// ============================================================
//
// 规则：
//   - 3 级及以上（Level ≤ 3）可以改、删、排任意待办
//   - 4 级只能改、删、排 created_by 为自己的待办；
//     被指派给自己的待办只能改状态
//   - created_by 为 NULL（旧数据或创建者已删除）只有 3 级及以上能动
//   - 5 级与非法等级一律拒绝（fail closed：Level=0 不是最高权限）

// todoActor 解析操作者的能力。
func todoActor(op *model.User) (canAll bool, err error) {
	if op == nil || !model.IsValidLevel(op.Level) || op.ID == 0 {
		return false, ErrForbidden
	}
	if op.HasLevel(model.LevelEditor) {
		return true, nil
	}
	if op.HasLevel(model.LevelMember) {
		return false, nil
	}
	return false, ErrForbidden
}

// onlyStatus 判断这次更新是否只改状态。
func (u TodoUpdate) onlyStatus() bool {
	return u.Title == nil && u.Priority == nil && !u.Description.Set && !u.DueDate.Set && !u.Assignee.Set
}

// ============================================================
// 创建
// ============================================================

// CreateTodoInput 是创建待办的输入。
type CreateTodoInput struct {
	Title       string
	Description string
	// Priority 值越大越靠前。
	Priority int
	DueDate  *time.Time
	// AssigneeID 可选；必须是启用中的用户。
	AssigneeID *uint64
}

// CreateTodo 创建新的待办事项，created_by 记为操作者。
func (s *TodoService) CreateTodo(op *model.User, in CreateTodoInput) (*model.Todo, error) {
	if _, err := todoActor(op); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, errors.New("待办标题不能为空")
	}
	if in.AssigneeID != nil {
		if err := s.checkAssignee(*in.AssigneeID); err != nil {
			return nil, err
		}
	}

	// 获取当前最大排序值
	var maxOrder int
	if err := s.db.Model(&model.Todo{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
		return nil, fmt.Errorf("查询待办排序失败: %w", err)
	}

	creator := op.ID
	todo := model.Todo{
		Title:       model.TruncateRunes(title, 500),
		Description: in.Description,
		Status:      "pending",
		Priority:    in.Priority,
		DueDate:     in.DueDate,
		SortOrder:   maxOrder + 1,
		CreatedBy:   &creator,
		AssigneeID:  in.AssigneeID,
	}

	if err := s.db.Create(&todo).Error; err != nil {
		return nil, fmt.Errorf("创建待办事项失败: %w", err)
	}
	list := []model.Todo{todo}
	if err := s.attachUsers(list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// checkAssignee 确认被指派者存在且启用。
func (s *TodoService) checkAssignee(id uint64) error {
	var n int64
	if err := s.db.Model(&model.User{}).Where("id = ? AND is_active = ?", id, true).Count(&n).Error; err != nil {
		return fmt.Errorf("查询被指派用户失败: %w", err)
	}
	if n == 0 {
		return ErrTodoBadAssignee
	}
	return nil
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
	// Assignee 清空时写入 NULL；设置时必须是启用中的用户。
	Assignee Optional[uint64]
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
	if u.Assignee.Set {
		if u.Assignee.Value == nil {
			updates["assignee_id"] = nil
		} else {
			updates["assignee_id"] = *u.Assignee.Value
		}
	}
	return updates, nil
}

// UpdateTodo 部分更新待办事项，按操作者等级校验归属。
//
// 4 级的归属条件直接写进 UPDATE 的 WHERE（created_by = me 或
// assignee_id = me），而不是「先查后改」：两步之间归属可能被别人改掉。
// RowsAffected 为 0 时再查一次，区分「不存在」（404）与「不是你的」（403）。
func (s *TodoService) UpdateTodo(op *model.User, id uint64, u TodoUpdate) error {
	canAll, err := todoActor(op)
	if err != nil {
		return err
	}
	updates, err := buildTodoUpdates(u)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	if u.Assignee.Set && u.Assignee.Value != nil {
		if err := s.checkAssignee(*u.Assignee.Value); err != nil {
			return err
		}
	}

	query := s.db.Model(&model.Todo{}).Where("id = ?", id)
	if !canAll {
		if u.onlyStatus() {
			query = query.Where("(created_by = ? OR assignee_id = ?)", op.ID, op.ID)
		} else {
			query = query.Where("created_by = ?", op.ID)
		}
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("更新待办事项失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return s.missingOrForbidden(id, canAll)
	}
	return nil
}

// missingOrForbidden 在带归属条件的写操作没命中时区分 404 与 403。
func (s *TodoService) missingOrForbidden(id uint64, canAll bool) error {
	if canAll {
		return ErrTodoNotFound
	}
	if _, err := s.loadTodo(id); err != nil {
		return err
	}
	return ErrForbidden
}

// ============================================================
// 删除
// ============================================================

// DeleteTodo 根据 ID 删除待办事项。4 级只能删自己创建的。
func (s *TodoService) DeleteTodo(op *model.User, id uint64) error {
	canAll, err := todoActor(op)
	if err != nil {
		return err
	}
	query := s.db.Where("id = ?", id)
	if !canAll {
		query = query.Where("created_by = ?", op.ID)
	}
	result := query.Delete(&model.Todo{})
	if result.Error != nil {
		return fmt.Errorf("删除待办事项失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return s.missingOrForbidden(id, canAll)
	}
	return nil
}

// ============================================================
// 排序
// ============================================================

// ReorderTodos 按传入的 ID 顺序更新待办的 sort_order。
//
// ids 为待办 ID 列表，按期望顺序排列。一条 UPDATE 完成，整体生效或整体不变。
//
// 4 级只有在**全部** id 都是自己创建的待办时才能执行，否则整体拒绝：
// 部分生效会让别人的待办被挤到奇怪的位置，而且结果取决于传了哪些 id。
func (s *TodoService) ReorderTodos(op *model.User, ids []uint64) error {
	canAll, err := todoActor(op)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if !canAll {
		uniq := make(map[uint64]struct{}, len(ids))
		for _, id := range ids {
			uniq[id] = struct{}{}
		}
		var owned int64
		if err := s.db.Model(&model.Todo{}).
			Where("id IN ? AND created_by = ?", ids, op.ID).
			Count(&owned).Error; err != nil {
			return fmt.Errorf("校验待办归属失败: %w", err)
		}
		if owned != int64(len(uniq)) {
			return ErrForbidden
		}
	}
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
	// ErrTodoBadAssignee 表示被指派用户不存在或已停用，handler 据此回 400。
	ErrTodoBadAssignee = errors.New("被指派的用户不存在或已停用")
)
