package service

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// BookmarkService 书签业务服务。
type BookmarkService struct {
	db *gorm.DB
}

// NewBookmarkService 创建 BookmarkService 实例。
func NewBookmarkService(db *gorm.DB) *BookmarkService {
	return &BookmarkService{db: db}
}

// ============================================================
// 书签分组操作
// ============================================================

// ListGroups 返回所有分组及其书签，按 sort_order 升序排列。
func (s *BookmarkService) ListGroups() ([]model.BookmarkGroup, error) {
	var groups []model.BookmarkGroup
	if err := s.db.Order("priority DESC, sort_order ASC").
		Preload("Bookmarks", func(db *gorm.DB) *gorm.DB {
			return db.Order("priority DESC, sort_order ASC")
		}).
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("查询分组列表失败: %w", err)
	}
	return groups, nil
}

// CreateGroup 创建新分组。
func (s *BookmarkService) CreateGroup(name string) (*model.BookmarkGroup, error) {
	if name == "" {
		return nil, errors.New("分组名称不能为空")
	}

	// 获取当前最大排序值
	var maxOrder int
	if err := s.db.Model(&model.BookmarkGroup{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
		return nil, fmt.Errorf("查询分组排序失败: %w", err)
	}

	group := model.BookmarkGroup{
		Name:      name,
		SortOrder: maxOrder + 1,
	}
	if err := s.db.Create(&group).Error; err != nil {
		return nil, fmt.Errorf("创建分组失败: %w", err)
	}
	return &group, nil
}

// DeleteGroup 删除分组及其下所有书签。
func (s *BookmarkService) DeleteGroup(id uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 先删书签
		if err := tx.Where("group_id = ?", id).Delete(&model.Bookmark{}).Error; err != nil {
			return fmt.Errorf("删除分组下书签失败: %w", err)
		}
		// 再删分组
		result := tx.Delete(&model.BookmarkGroup{}, id)
		if result.Error != nil {
			return fmt.Errorf("删除分组失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrGroupNotFound
		}
		return nil
	})
}

// ReorderGroups 按传入的 ID 顺序更新分组的 sort_order。
//
// ids 为分组 ID 列表，按期望顺序排列。
func (s *BookmarkService) ReorderGroups(ids []uint64) error {
	if err := reorderBySortOrder(s.db, "bookmark_groups", ids, ""); err != nil {
		return fmt.Errorf("更新分组排序失败: %w", err)
	}
	return nil
}

// ============================================================
// 书签操作
// ============================================================

// ListBookmarks 按分组返回书签列表（嵌套结构）。
//
// 返回所有分组，每个分组包含其下的书签，按 sort_order 排序。
func (s *BookmarkService) ListBookmarks() ([]model.BookmarkGroup, error) {
	var groups []model.BookmarkGroup
	// 使用 Preload 一次性加载关联书签，避免 N+1 查询
	if err := s.db.Order("priority DESC, sort_order ASC").
		Preload("Bookmarks", func(db *gorm.DB) *gorm.DB {
			return db.Order("priority DESC, sort_order ASC")
		}).
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("查询分组列表失败: %w", err)
	}
	return groups, nil
}

// CreateBookmark 在指定分组下创建书签。
func (s *BookmarkService) CreateBookmark(groupID uint64, title, url, icon string) (*model.Bookmark, error) {
	if title == "" {
		return nil, errors.New("书签标题不能为空")
	}
	if url == "" {
		return nil, errors.New("书签 URL 不能为空")
	}

	// URL scheme 校验（防 XSS：禁止 javascript:/data:/file: 等危险协议）
	if err := validateBookmarkURL(url); err != nil {
		return nil, err
	}

	// 验证分组存在
	var count int64
	s.db.Model(&model.BookmarkGroup{}).Where("id = ?", groupID).Count(&count)
	if count == 0 {
		return nil, ErrGroupNotFound
	}

	// 获取该分组下最大排序值
	var maxOrder int
	if err := s.db.Model(&model.Bookmark{}).Where("group_id = ?", groupID).
		Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
		return nil, fmt.Errorf("查询书签排序失败: %w", err)
	}

	bookmark := model.Bookmark{
		GroupID:   groupID,
		Title:     title,
		URL:       url,
		Icon:      icon,
		SortOrder: maxOrder + 1,
	}
	if err := s.db.Create(&bookmark).Error; err != nil {
		return nil, fmt.Errorf("创建书签失败: %w", err)
	}
	return &bookmark, nil
}

// BookmarkUpdate 是书签的部分更新：nil 表示不改该字段。
//
// Icon 用指针而不是空串表示「未提供」：旧签名把 icon 无条件写入，
// 只改标题也会顺手清空已有图标。
type BookmarkUpdate struct {
	GroupID  *uint64
	Title    *string
	URL      *string
	Icon     *string
	Priority *int
}

// UpdateBookmark 按字段更新书签。
//
// URL 与创建时走同一套 scheme 校验；跨分组移动时追加到目标分组末尾。
func (s *BookmarkService) UpdateBookmark(id uint64, req BookmarkUpdate) error {
	updates := map[string]interface{}{}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return fmt.Errorf("%w：标题不能为空", ErrInvalidBookmark)
		}
		updates["title"] = title
	}
	if req.URL != nil {
		if err := validateBookmarkURL(*req.URL); err != nil {
			return fmt.Errorf("%w：%s", ErrInvalidBookmark, err.Error())
		}
		updates["url"] = strings.TrimSpace(*req.URL)
	}
	if req.Icon != nil {
		updates["icon"] = strings.TrimSpace(*req.Icon)
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.GroupID != nil {
		var count int64
		if err := s.db.Model(&model.BookmarkGroup{}).Where("id = ?", *req.GroupID).Count(&count).Error; err != nil {
			return fmt.Errorf("查询分组失败: %w", err)
		}
		if count == 0 {
			return ErrGroupNotFound
		}
		var maxOrder int
		if err := s.db.Model(&model.Bookmark{}).Where("group_id = ?", *req.GroupID).
			Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
			return fmt.Errorf("查询书签排序失败: %w", err)
		}
		updates["group_id"] = *req.GroupID
		updates["sort_order"] = maxOrder + 1
	}

	if len(updates) == 0 {
		return nil
	}

	result := s.db.Model(&model.Bookmark{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("更新书签失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrBookmarkNotFound
	}
	return nil
}

// UpdateGroup 更新分组名称和优先级。
func (s *BookmarkService) UpdateGroupWithPriority(id uint64, name string, priority *int) error {
	if name == "" {
		return errors.New("分组名称不能为空")
	}
	updates := map[string]interface{}{"name": name}
	if priority != nil {
		updates["priority"] = *priority
	}
	result := s.db.Model(&model.BookmarkGroup{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("更新分组失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// DeleteBookmark 删除书签。
func (s *BookmarkService) DeleteBookmark(id uint64) error {
	result := s.db.Delete(&model.Bookmark{}, id)
	if result.Error != nil {
		return fmt.Errorf("删除书签失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrBookmarkNotFound
	}
	return nil
}

// ReorderBookmarks 按传入的 ID 顺序更新指定分组下书签的 sort_order。
//
// 限定 group_id：不属于该分组的 ID 不会被改动。
func (s *BookmarkService) ReorderBookmarks(groupID uint64, ids []uint64) error {
	if err := reorderBySortOrder(s.db, "bookmarks", ids, "group_id = ?", groupID); err != nil {
		return fmt.Errorf("更新书签排序失败: %w", err)
	}
	return nil
}

// reorderBySortOrder 用一条 UPDATE ... CASE 把 ids 依次设为 sort_order 1..n。
//
// 逐行 UPDATE 是 n 次往返，且要靠事务才能保证「要么全改要么不改」；
// 单条语句天然原子。table 只能是调用方写死的常量（不接受外部输入）；
// extraWhere 为附加条件（可为空），extraArgs 是它的参数。
//
// 超过 maxReorderIDs 个 id 返回 ErrReorderTooManyIDs，不发 SQL。
func reorderBySortOrder(db *gorm.DB, table string, ids []uint64, extraWhere string, extraArgs ...any) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxReorderIDs {
		return ErrReorderTooManyIDs
	}

	var sb strings.Builder
	args := make([]any, 0, len(ids)*3+1+len(extraArgs))
	sb.WriteString("UPDATE ")
	sb.WriteString(table)
	sb.WriteString(" SET sort_order = CASE id")
	// 参数在 CASE 分支里没有上下文类型，PostgreSQL 会推断成 text 并拒绝
	// 赋给 bigint 列（42804），所以显式转换。
	for i, id := range ids {
		sb.WriteString(" WHEN ?::bigint THEN ?::bigint")
		args = append(args, id, i+1)
	}
	sb.WriteString(" END, updated_at = ? WHERE id IN ?")
	args = append(args, time.Now(), ids)
	sb.WriteString(" AND deleted_at IS NULL")
	if extraWhere != "" {
		sb.WriteString(" AND ")
		sb.WriteString(extraWhere)
		args = append(args, extraArgs...)
	}
	return db.Exec(sb.String(), args...).Error
}

// validateBookmarkURL 校验书签 URL 的协议安全性。
//
// 仅允许 http/https 协议，禁止 javascript:/data:/file: 等危险协议，
// 防止存储型 XSS 攻击。
func validateBookmarkURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return errors.New("无效的 URL 格式")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("不支持的 URL 协议: %s（仅允许 http/https）", scheme)
	}
	return nil
}

// 书签相关业务错误。
var (
	ErrGroupNotFound    = errors.New("分组不存在")
	ErrBookmarkNotFound = errors.New("书签不存在")
	// ErrInvalidBookmark 包裹所有输入校验失败（空标题、危险 URL），
	// 让 handler 能区分 400 与存储层的 500。
	ErrInvalidBookmark = errors.New("书签参数无效")
)

// maxReorderIDs 是单次排序请求的 id 数量上限。
//
// reorderBySortOrder 每个 id 占 3 个绑定参数，PostgreSQL 单条语句最多
// 65535 个参数；1000 远低于该上限，也远多于界面上一屏能拖动的条目。
const maxReorderIDs = 1000

// ErrReorderTooManyIDs 表示排序请求的 id 超过上限，handler 据此回 400。
var ErrReorderTooManyIDs = fmt.Errorf("单次排序最多 %d 项", maxReorderIDs)
