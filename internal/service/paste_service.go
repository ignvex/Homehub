package service

import (
	"context"
	"errors"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// ErrPasteDisabled 表示配置中关闭了剪贴板模块。
var ErrPasteDisabled = errors.New("剪贴板模块已禁用")

// ErrPasteTokenMismatch 表示删除密钥缺失或不匹配。
var ErrPasteTokenMismatch = errors.New("删除密钥不正确")

// pasteEnabled 报告当前配置是否启用了剪贴板。
//
// 配置未加载时按禁用处理：模块默认关闭，避免意外暴露公开写入接口。
func pasteEnabled() bool {
	cfg := config.Get()
	return cfg != nil && cfg.Pastebin.Enabled
}

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
	if !pasteEnabled() {
		return nil, "", ErrPasteDisabled
	}

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
	if !pasteEnabled() {
		return nil, ErrPasteDisabled
	}

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

// DeletePaste 通过公开 ID + 删除密钥删除粘贴。
//
// 必须提供创建时下发的 delete_token：public_id 会出现在 URL 与列表中，
// 单凭它无法证明请求者就是创建者。
//
// 使用 Unscoped 做物理删除：Paste 是带 TTL 的临时内容，
// 软删除会让数据永久滞留在表里。
//
// 返回 ErrPasteTokenMismatch 表示 ID 存在但密钥不符；为免探测，
// 调用方对外应统一表现为 404。
func (s *PasteService) DeletePaste(publicID, token string) error {
	if !pasteEnabled() {
		return ErrPasteDisabled
	}
	if token == "" {
		return ErrPasteTokenMismatch
	}

	result := s.db.Unscoped().
		Where("public_id = ? AND delete_token = ?", publicID, token).
		Delete(&model.Paste{})
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
	if !pasteEnabled() {
		return nil, ErrPasteDisabled
	}

	var pastes []model.Paste
	if err := s.db.Where("expires_at > ?", time.Now()).
		Order("created_at DESC").Limit(50).
		Find(&pastes).Error; err != nil {
		return nil, err
	}
	return pastes, nil
}

// PurgeExpired 物理删除所有已过期的粘贴。
//
// 使用 Unscoped：Paste 嵌入 BaseModel 因而带 gorm.DeletedAt，
// 不加 Unscoped 的 Delete 只会写 deleted_at，过期数据永不真正清理、
// 表会持续膨胀。这里连之前被软删的行一并物理删除。
func (s *PasteService) PurgeExpired() (int64, error) {
	result := s.db.Unscoped().Where("expires_at <= ?", time.Now()).Delete(&model.Paste{})
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
