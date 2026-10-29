package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// ============================================================
// LogService — 系统日志查询服务
// ============================================================

// LogQuery 日志列表查询条件。
type LogQuery struct {
	Page, PerPage int
	Level         string
	Q             string
	Since, Until  time.Time
}

// LogService 提供日志条目的查询功能。
type LogService struct {
	db *gorm.DB
}

// NewLogService 创建 LogService 实例。
func NewLogService(db *gorm.DB) *LogService {
	return &LogService{db: db}
}

// validLogLevels 日志级别白名单。
var validLogLevels = map[string]bool{
	"ERROR": true,
	"WARN":  true,
	"INFO":  true,
	"DEBUG": true,
}

// clampPerPage 限定每页数量到 1-100，非正值回落到 20。
func clampPerPage(perPage int) int {
	if perPage <= 0 {
		return 20
	}
	if perPage > 100 {
		return 100
	}
	return perPage
}

// normalizePage 保证页码 >= 1。
func normalizePage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

// ListLogs 分页查询日志条目，支持按级别、关键词和时间范围筛选。
func (s *LogService) ListLogs(q LogQuery) ([]model.LogEntry, int64, error) {
	q.PerPage = clampPerPage(q.PerPage)
	q.Page = normalizePage(q.Page)

	query := s.db.Model(&model.LogEntry{})

	// 级别筛选
	q.Level = strings.TrimSpace(strings.ToUpper(q.Level))
	if q.Level != "" {
		if validLogLevels[q.Level] {
			query = query.Where("level = ?", q.Level)
		}
	}

	// 关键词模糊搜索
	if strings.TrimSpace(q.Q) != "" {
		query = query.Where("message ILIKE ?", "%"+strings.TrimSpace(q.Q)+"%")
	}

	// 时间范围筛选
	if !q.Since.IsZero() {
		query = query.Where("log_time >= ?", q.Since)
	}
	if !q.Until.IsZero() {
		query = query.Where("log_time <= ?", q.Until)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("查询日志总数失败: %w", err)
	}

	var entries []model.LogEntry
	offset := (q.Page - 1) * q.PerPage
	if err := query.Order("log_time DESC").Offset(offset).Limit(q.PerPage).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("查询日志列表失败: %w", err)
	}

	return entries, total, nil
}

// DeleteAll 清除所有日志记录。
func (s *LogService) DeleteAll() error {
	if err := s.db.Where("1 = 1").Delete(&model.LogEntry{}).Error; err != nil {
		return fmt.Errorf("清除日志失败: %w", err)
	}
	return nil
}
