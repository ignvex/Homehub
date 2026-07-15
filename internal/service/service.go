// Package service 提供 HomeHub 业务逻辑层。
//
// 遵循 handler → service → model 三层架构，
// 各 Service 通过构造函数注入 *gorm.DB 依赖，
// 避免隐式全局依赖，方便单元测试 mock。
package service

import (
	"gorm.io/gorm"
)

// Services 聚合所有业务服务，由 main.go 统一初始化后注入 Handler。
type Services struct {
	Auth       *AuthService
	Bookmark   *BookmarkService
	Config     *ConfigService
	Media      *MediaService
	Background *BackgroundService
	Gravatar   *GravatarService
	Favicon    *FaviconService
	RSS        *RSSService
	Search     *SearchService
	Log        *LogService
	Todo       *TodoService
	Balance    *BalanceService
	Paste      *PasteService
}

// NewServices 创建并返回所有 Service 实例。
func NewServices(db *gorm.DB) *Services {
	return &Services{
		Auth:       NewAuthService(db),
		Bookmark:   NewBookmarkService(db),
		Config:     NewConfigService(),
		Media:      NewMediaService(db),
		Background: NewBackgroundService(),
		Gravatar:   NewGravatarService(db),
		Favicon:    NewFaviconService(db),
		RSS:        NewRSSService(db),
		Search:     NewSearchService(db),
		Log:        NewLogService(db),
		Todo:       NewTodoService(db),
		Balance:    NewBalanceService(),
		Paste:      NewPasteService(db),
	}
}
