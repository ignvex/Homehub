package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// ============================================================
// LogService — 系统日志查询服务
// ============================================================

// LogQuery 日志列表查询条件。
//
// 级别筛选有三种写法，按优先级依次生效：
//   - Levels：显式多选，如 ["ERROR","WARN"]；
//   - MinLevel：下限，如 "WARN" 表示 WARN 及以上（含 ERROR）；
//   - Level：单个精确值，兼容旧调用方。
type LogQuery struct {
	Page, PerPage int
	Level         string
	Levels        []string
	MinLevel      string
	Q             string
	Since, Until  time.Time
}

// LogService 提供日志条目的查询与保留期清理。
type LogService struct {
	db *gorm.DB

	// afterPurge 在每轮保留期检查结束后调用，仅供测试同步。
	afterPurge func()
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

// levelRank 是级别的严重程度序。
//
// 有了它，“至少 WARN”就能翻译成“rank >= 3”或者一组 IN 值，
// 不必为每个组合手写条件。数值只用于比较，不对外暴露。
var levelRank = map[string]int{
	"DEBUG": 1,
	"INFO":  2,
	"WARN":  3,
	"ERROR": 4,
}

// ParseLevels 把逗号分隔的级别串解析成去重、大写、有序的列表。
//
// 返回 error 而不是静默丢非法值：调用方传 level=FOO 时应当得到 400，
// 而不是“看起来过滤了、实际返回全部日志”——后者会让人误判线上状态。
func ParseLevels(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	seen := make(map[string]struct{}, 4)
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		lv := strings.ToUpper(strings.TrimSpace(part))
		if lv == "" {
			continue
		}
		if !validLogLevels[lv] {
			return nil, fmt.Errorf("无效的日志级别 %q（可选：DEBUG/INFO/WARN/ERROR）", part)
		}
		if _, dup := seen[lv]; dup {
			continue
		}
		seen[lv] = struct{}{}
		out = append(out, lv)
	}
	return out, nil
}

// LevelsAtLeast 返回严重程度不低于 min 的全部级别。
func LevelsAtLeast(min string) ([]string, error) {
	min = strings.ToUpper(strings.TrimSpace(min))
	if min == "" {
		return nil, nil
	}
	rank, ok := levelRank[min]
	if !ok {
		return nil, fmt.Errorf("无效的最低日志级别 %q（可选：DEBUG/INFO/WARN/ERROR）", min)
	}
	out := make([]string, 0, 4)
	for lv, r := range levelRank {
		if r >= rank {
			out = append(out, lv)
		}
	}
	return out, nil
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

	// 级别筛选。三种写法合并成一个 IN 条件，避免多层嵌套 WHERE。
	wanted := q.Levels
	if len(wanted) == 0 && q.MinLevel != "" {
		levels, err := LevelsAtLeast(q.MinLevel)
		if err != nil {
			return nil, 0, err
		}
		wanted = levels
	}
	if len(wanted) == 0 && q.Level != "" {
		single, err := ParseLevels(q.Level)
		if err != nil {
			return nil, 0, err
		}
		wanted = single
	}
	if len(wanted) > 0 {
		query = query.Where("level IN ?", wanted)
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

// PurgeOlderThan 物理删除 log_time 早于 cutoff 的日志，返回删除行数。
//
// 用 Unscoped：软删除只会打标记，表不会变小，保留期就失去了意义。
// 按 log_time 而不是 created_at：数据库写入是批量的，created_at 可能
// 比事件实际发生晚。
func (s *LogService) PurgeOlderThan(cutoff time.Time) (int64, error) {
	result := s.db.Unscoped().Where("log_time < ?", cutoff).Delete(&model.LogEntry{})
	if result.Error != nil {
		return 0, fmt.Errorf("清理过期日志失败: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// logRetentionInterval 是保留期清理的执行间隔。
const logRetentionInterval = 6 * time.Hour

// logRetentionCutoff 把保留天数换算成截止时间；days <= 0 表示不清理。
func logRetentionCutoff(days int, now time.Time) (time.Time, bool) {
	if days <= 0 {
		return time.Time{}, false
	}
	return now.AddDate(0, 0, -days), true
}

// StartRetentionLoop 启动日志保留期清理：启动时跑一次，之后每 6 小时一次。
//
// 每轮重新读取 log.retention_days，热更新在下一轮生效；为 0 时跳过。
// goroutine 登记在 wg 上，ctx 取消时退出。
func (s *LogService) StartRetentionLoop(ctx context.Context, wg *sync.WaitGroup) {
	wg.Go(func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("[log] 日志保留期清理已停止")
				return
			case <-timer.C:
			}

			days := 0
			if cfg := config.Get(); cfg != nil {
				days = cfg.Log.RetentionDays
			}
			if cutoff, ok := logRetentionCutoff(days, time.Now()); ok {
				if n, err := s.PurgeOlderThan(cutoff); err != nil {
					logger.Warn("[log] 日志保留期清理失败", "error", err)
				} else if n > 0 {
					logger.Info("[log] 已清理过期日志", "deleted", n, "retention_days", days)
				}
			}
			if s.afterPurge != nil {
				s.afterPurge()
			}

			timer.Reset(logRetentionInterval)
		}
	})
}
