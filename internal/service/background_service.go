package service

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
)

// BackgroundService 背景设置业务服务。
type BackgroundService struct{}

// NewBackgroundService 创建 BackgroundService 实例。
func NewBackgroundService() *BackgroundService {
	return &BackgroundService{}
}

// BackgroundSettings 对外暴露的背景设置（JSON 视图）。
type BackgroundSettings struct {
	Mode             string   `json:"mode"`
	IntervalSeconds  int      `json:"interval_seconds"`
	TransitionEffect string   `json:"transition_effect"`
	FadeDurationMs   int      `json:"fade_duration_ms"`
	SelectedAssetIDs []uint64 `json:"selected_asset_ids"`
}

// Get 返回当前背景设置。
func (s *BackgroundService) Get() BackgroundSettings {
	cfg := config.Get()
	if cfg == nil {
		return BackgroundSettings{
			Mode:             "single",
			IntervalSeconds:  30,
			TransitionEffect: "fade",
			FadeDurationMs:   1000,
		}
	}

	return BackgroundSettings{
		Mode:             cfg.Background.Mode,
		IntervalSeconds:  cfg.Background.IntervalSeconds,
		TransitionEffect: cfg.Background.TransitionEffect,
		FadeDurationMs:   cfg.Background.FadeDurationMs,
		SelectedAssetIDs: cfg.Background.SelectedAssetIDs,
	}
}

// UpdateBackgroundRequest 更新背景设置的请求体。
type UpdateBackgroundRequest struct {
	Mode             *string   `json:"mode,omitempty" form:"mode"`
	IntervalSeconds  *int      `json:"interval_seconds,omitempty" form:"interval_seconds"`
	TransitionEffect *string   `json:"transition_effect,omitempty" form:"transition_effect"`
	FadeDurationMs   *int      `json:"fade_duration_ms,omitempty" form:"fade_duration_ms"`
	SelectedAssetIDs *[]uint64 `json:"selected_asset_ids,omitempty" form:"selected_asset_ids"`
}

// Update 更新背景设置并写回 config.json。
func (s *BackgroundService) Update(req UpdateBackgroundRequest) error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("配置未加载")
	}

	merged := *cfg

	if req.Mode != nil {
		merged.Background.Mode = *req.Mode
	}
	if req.IntervalSeconds != nil {
		merged.Background.IntervalSeconds = *req.IntervalSeconds
	}
	if req.TransitionEffect != nil {
		merged.Background.TransitionEffect = *req.TransitionEffect
	}
	if req.FadeDurationMs != nil {
		merged.Background.FadeDurationMs = *req.FadeDurationMs
	}
	if req.SelectedAssetIDs != nil {
		merged.Background.SelectedAssetIDs = *req.SelectedAssetIDs
	}

	// 写回 config.json
	data, err := json.MarshalIndent(merged, "", "    ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile("config.json", data, 0600); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	// 触发热重载
	if err := config.Load("config.json"); err != nil {
		return fmt.Errorf("重新加载配置失败: %w", err)
	}

	logger.Info("背景设置已更新并热重载")
	return nil
}
