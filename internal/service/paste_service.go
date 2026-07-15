package service

import (
	"context"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// PasteService 剪贴板服务。
//
// 提供粘贴内容的创建、获取、删除和过期清理功能。
// 完全不要求认证——创建、读取、删除均公开。
type PasteService struct {
	db *gorm.DB
}

// NewPasteService 创建 PasteService 实例。
func NewPasteService(db *gorm.DB) *PasteService {
	return &PasteService{db: db}
}

// CreatePaste 创建一条新的粘贴。
//
// 返回的 Paste 包含 public_id（访客访问标识）和 delete_token（删除密钥）。
// 调用方应将 delete_token 一并返回给创建者，不对外暴露。
func (s *PasteService) CreatePaste(content string) (*model.Paste, string, error) {
	cfg := config.Get()
	ttlHours := 168
	if cfg != nil && cfg.Pastebin.DefaultTTLHours > 0 {
		ttlHours = cfg.Pastebin.DefaultTTLHours
	}

	paste := &model.Paste{
		PublicID:    model.NanoID(8),
		Content:     content,
		DeleteToken: model.RandomToken(16), // 32 字符十六进制
		ExpiresAt:   time.Now().Add(time.Duration(ttlHours) * time.Hour),
	}

	if err := s.db.Create(paste).Error; err != nil {
		return nil, "", err
	}

	return paste, paste.DeleteToken, nil
}

// GetPaste 通过公开 ID 获取粘贴内容。
//
// 不会返回 delete_token，只返回内容 + 元数据。
// 若已过期则返回 ErrNotFound。
func (s *PasteService) GetPaste(publicID string) (*model.Paste, error) {
	var paste model.Paste
	if err := s.db.Where("public_id = ?", publicID).First(&paste).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, err
		}
		return nil, err
	}

	if time.Now().After(paste.ExpiresAt) {
		return nil, gorm.ErrRecordNotFound
	}

	return &paste, nil
}

// DeletePaste 通过公开 ID 删除粘贴（无需密钥）。
func (s *PasteService) DeletePaste(publicID string) error {
	result := s.db.Where("public_id = ?", publicID).Delete(&model.Paste{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListPastes 返回最近创建的粘贴摘要列表（不含 content 和 delete_token）。
//
// 按创建时间倒序，最多返回 50 条。已过期的排除。
func (s *PasteService) ListPastes() ([]model.Paste, error) {
	var pastes []model.Paste
	if err := s.db.Where("expires_at > ?", time.Now()).
		Order("created_at DESC").Limit(50).
		Find(&pastes).Error; err != nil {
		return nil, err
	}
	return pastes, nil
}

// PurgeExpired 硬删除所有已过期的粘贴。
func (s *PasteService) PurgeExpired() (int64, error) {
	result := s.db.Where("expires_at <= ?", time.Now()).Delete(&model.Paste{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// StartPurgeLoop 启动后台定期清理过期粘贴。
//
// ctx 用于生命周期控制。清理间隔由 config 控制。
func (s *PasteService) StartPurgeLoop(ctx context.Context) {
	cfg := config.Get()
	intervalHours := 24
	if cfg != nil && cfg.Pastebin.CleanupIntervalHours > 0 {
		intervalHours = cfg.Pastebin.CleanupIntervalHours
	}
	interval := time.Duration(intervalHours) * time.Hour

	logger.Info("[pastebin] 过期清理已启动", "interval", interval.String())

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("[pastebin] 过期清理已停止")
				return
			case <-ticker.C:
				deleted, err := s.PurgeExpired()
				if err != nil {
					logger.Warn("[pastebin] 过期清理失败", "error", err)
				} else if deleted > 0 {
					logger.Info("[pastebin] 过期清理完成", "deleted", deleted)
				}
			}
		}
	}()
}
