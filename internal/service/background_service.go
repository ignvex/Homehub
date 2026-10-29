package service

import (
	"github.com/ignvex/HomeHub/internal/config"
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

// Get 返回当前背景设置（首页背景）。
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
		Mode:             cfg.WebUI.Home.Background.Mode,
		IntervalSeconds:  cfg.WebUI.Home.Background.IntervalSeconds,
		TransitionEffect: cfg.WebUI.Home.Background.TransitionEffect,
		FadeDurationMs:   cfg.WebUI.Home.Background.FadeDurationMs,
		SelectedAssetIDs: cfg.WebUI.Home.Background.SelectedAssetIDs,
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

// Update 更新首页背景设置并写回当前配置文件。
// 使用 AtomicJSONUpdate 保留 JSON 中的非结构体字段（如 _comment）。
func (s *BackgroundService) Update(req UpdateBackgroundRequest) error {
	return config.AtomicJSONUpdate(config.Path(), func(raw map[string]any) error {
		webui, ok := raw["webui"].(map[string]any)
		if !ok {
			raw["webui"] = map[string]any{}
			webui = raw["webui"].(map[string]any)
		}
		home, ok := webui["home"].(map[string]any)
		if !ok {
			webui["home"] = map[string]any{}
			home = webui["home"].(map[string]any)
		}
		bg, ok := home["background"].(map[string]any)
		if !ok {
			home["background"] = map[string]any{}
			bg = home["background"].(map[string]any)
		}

		if req.Mode != nil {
			bg["mode"] = *req.Mode
		}
		if req.IntervalSeconds != nil {
			bg["interval_seconds"] = float64(*req.IntervalSeconds)
		}
		if req.TransitionEffect != nil {
			bg["transition_effect"] = *req.TransitionEffect
		}
		if req.FadeDurationMs != nil {
			bg["fade_duration_ms"] = float64(*req.FadeDurationMs)
		}
		if req.SelectedAssetIDs != nil {
			bg["selected_asset_ids"] = *req.SelectedAssetIDs
		}
		return nil
	})
}
