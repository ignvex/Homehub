package model

// AutoMigrateModels 返回交给 GORM AutoMigrate 管理的全部模型，按迁移顺序排列。
//
// 启动流程（internal/app）与集成测试（internal/database）都从这里取列表。
// 两处各写一份时曾经漂移：新增 BalanceTarget 只改了启动路径，集成测试
// 的 schema 里就缺了 balance_targets，TestIntegrationTablesCreated 失败。
//
// pastes 不在其中：它带建表选项，由 CreatePastesTable 单独创建。
func AutoMigrateModels() []any {
	return []any{
		&User{},
		&Session{},
		&BookmarkGroup{},
		&Bookmark{},
		&LogEntry{},
		&MediaAsset{},
		&AvatarCache{},
		&RSSGroup{},
		&RSSFeed{},
		&RSSEntry{},
		&RSSEntryFeed{},
		&RSSImage{},
		&RSSImageSource{},
		&RSSImageRef{},
		&RSSEntryState{},
		&Todo{},
		&BalanceTarget{},
	}
}
