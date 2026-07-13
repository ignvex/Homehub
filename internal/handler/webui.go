package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/config"
)

type publicAsset struct {
	ID        uint64 `json:"id"`
	FilePath  string `json:"file_path"`
	MediaType string `json:"media_type"`
}

type publicPage struct {
	Background config.BackgroundConfig `json:"background"`
	Layout     string                  `json:"layout,omitempty"`
	Assets     []publicAsset           `json:"assets"`
}

type publicWebUI struct {
	Home  publicPage `json:"home"`
	Login publicPage `json:"login"`
}

func WebUIConfig(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		Fail(c, 503, CodeInternalError, "配置尚未加载")
		return
	}

	build := func(background config.BackgroundConfig, layout string) (publicPage, error) {
		resolved, err := svc.Media.ListByIDs(background.SelectedAssetIDs)
		if err != nil {
			return publicPage{}, err
		}
		assets := make([]publicAsset, 0, len(resolved))
		for _, asset := range resolved {
			assets = append(assets, publicAsset{ID: asset.ID, FilePath: asset.FilePath, MediaType: asset.MediaType})
		}
		return publicPage{Background: background, Layout: layout, Assets: assets}, nil
	}

	home, err := build(cfg.WebUI.Home.Background, "")
	if err != nil {
		FailInternal(c, "查询首页背景资源失败")
		return
	}
	login, err := build(cfg.WebUI.Login.Background, cfg.WebUI.Login.Layout)
	if err != nil {
		FailInternal(c, "查询登录页背景资源失败")
		return
	}
	Success(c, publicWebUI{Home: home, Login: login})
}
