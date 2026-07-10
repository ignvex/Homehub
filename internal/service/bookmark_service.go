package service

import (
	"errors"
	"fmt"

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

// ListGroups 返回所有分组，按 sort_order 升序排列。
func (s *BookmarkService) ListGroups() ([]model.BookmarkGroup, error) {
	var groups []model.BookmarkGroup
	if err := s.db.Order("sort_order ASC").Find(&groups).Error; err != nil {
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
	s.db.Model(&model.BookmarkGroup{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)

	group := model.BookmarkGroup{
		Name:      name,
		SortOrder: maxOrder + 1,
	}
	if err := s.db.Create(&group).Error; err != nil {
		return nil, fmt.Errorf("创建分组失败: %w", err)
	}
	return &group, nil
}

// UpdateGroup 更新分组名称。
func (s *BookmarkService) UpdateGroup(id uint64, name string) error {
	if name == "" {
		return errors.New("分组名称不能为空")
	}
	result := s.db.Model(&model.BookmarkGroup{}).Where("id = ?", id).Update("name", name)
	if result.Error != nil {
		return fmt.Errorf("更新分组失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrGroupNotFound
	}
	return nil
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
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.BookmarkGroup{}).Where("id = ?", id).
				Update("sort_order", i+1).Error; err != nil {
				return fmt.Errorf("更新分组排序失败: %w", err)
			}
		}
		return nil
	})
}

// ============================================================
// 书签操作
// ============================================================

// ListBookmarks 按分组返回书签列表（嵌套结构）。
//
// 返回所有分组，每个分组包含其下的书签，按 sort_order 排序。
func (s *BookmarkService) ListBookmarks() ([]model.BookmarkGroup, error) {
	var groups []model.BookmarkGroup
	if err := s.db.Order("sort_order ASC").Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("查询分组列表失败: %w", err)
	}

	for i := range groups {
		var bookmarks []model.Bookmark
		if err := s.db.Where("group_id = ?", groups[i].ID).
			Order("sort_order ASC").Find(&bookmarks).Error; err != nil {
			return nil, fmt.Errorf("查询书签列表失败: %w", err)
		}
		groups[i].Bookmarks = bookmarks
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

	// 验证分组存在
	var count int64
	s.db.Model(&model.BookmarkGroup{}).Where("id = ?", groupID).Count(&count)
	if count == 0 {
		return nil, ErrGroupNotFound
	}

	// 获取该分组下最大排序值
	var maxOrder int
	s.db.Model(&model.Bookmark{}).Where("group_id = ?", groupID).
		Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)

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

// UpdateBookmark 更新书签信息。
func (s *BookmarkService) UpdateBookmark(id uint64, title, url, icon string) error {
	updates := map[string]interface{}{}
	if title != "" {
		updates["title"] = title
	}
	if url != "" {
		updates["url"] = url
	}
	// icon 可为空字符串，允许清空图标
	updates["icon"] = icon

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
func (s *BookmarkService) ReorderBookmarks(groupID uint64, ids []uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.Bookmark{}).Where("id = ? AND group_id = ?", id, groupID).
				Update("sort_order", i+1).Error; err != nil {
				return fmt.Errorf("更新书签排序失败: %w", err)
			}
		}
		return nil
	})
}

// 书签相关业务错误。
var (
	ErrGroupNotFound    = errors.New("分组不存在")
	ErrBookmarkNotFound = errors.New("书签不存在")
)
