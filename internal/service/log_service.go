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

// ListLogs 分页查询日志条目，支持按级别、关键词和时间范围筛选。
//
// 参数：
//   - page, perPage: 分页，perPage 最大 100
//   - level: 日志级别筛选（ERROR/WARN/INFO/DEBUG），空字符串表示不过滤
//   - q: 消息内容模糊搜索（ILIKE）
//   - since: 日志时间范围起点（零值表示不过滤）
//   - until: 日志时间范围终点（零值表示不过滤）
//
// 结果按 log_time DESC 排序。
func (s *LogService) ListLogs(page, perPage int, level string, q string, since, until time.Time) ([]model.LogEntry, int64, error) {
	perPage = clampPerPage(perPage)
	page = normalizePage(page)

	query := s.db.Model(&model.LogEntry{})

	// 级别筛选
	level = strings.TrimSpace(strings.ToUpper(level))
	if level != "" {
		if validLogLevels[level] {
			query = query.Where("level = ?", level)
		}
	}

	// 关键词模糊搜索
	if strings.TrimSpace(q) != "" {
		query = query.Where("message ILIKE ?", "%"+strings.TrimSpace(q)+"%")
	}

	// 时间范围筛选
	if !since.IsZero() {
		query = query.Where("log_time >= ?", since)
	}
	if !until.IsZero() {
		query = query.Where("log_time <= ?", until)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("查询日志总数失败: %w", err)
	}

	var entries []model.LogEntry
	offset := (page - 1) * perPage
	if err := query.Order("log_time DESC").Offset(offset).Limit(perPage).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("查询日志列表失败: %w", err)
	}

	return entries, total, nil
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
