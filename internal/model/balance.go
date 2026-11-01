package model

// ============================================================
// BalanceTarget — LLM 余额监控点
// ============================================================

// BalanceTarget 是一个余额监控点。
//
// 这些记录原先放在 config.json 的 balance_monitor.targets 里。搬进数据库是
// 因为监控点需要增删改：每次加一个供应商都要 SSH 改配置并等热重载，而
// 「余额监控」本身是个会随用随加的功能，不是一锤定音的部署参数。
//
// 模块总开关与刷新间隔仍留在 config.json：它们属于部署参数，
// 和监控点列表的生命周期不同（前者由运维定，后者由使用者随时改）。
//
// 表名：balance_targets
type BalanceTarget struct {
	BaseModel
	Name    string `gorm:"size:120;not null" json:"name"`      // 展示名称，如 "DeepSeek 主号"
	Type    string `gorm:"size:32;not null;index" json:"type"` // 供应商类型，目前仅 deepseek
	BaseURL string `gorm:"size:500;not null" json:"base_url"`  // API 基础地址
	// APIKey 明文存储。与 config.json 里的其他凭据一致：这是个人单机部署，
	// 加密需要一把同样落在磁盘上的密钥，只是把问题挪一层。
	// 真正的约束是它**永不通过 API 返回**——读取时一律脱敏成掩码。
	APIKey    string `gorm:"type:text" json:"-"` // 不在 JSON 里出现，见 BalanceTargetView
	Enabled   bool   `gorm:"not null;default:true" json:"enabled"`
	SortOrder int    `gorm:"not null;default:0" json:"sort_order"` // 值小的排前面
}

// TableName 指定表名。
func (BalanceTarget) TableName() string {
	return "balance_targets"
}

// BalanceTargetTypes 返回受支持的供应商类型。
//
// 加新供应商时改这里，并在 service 的 fetch 分派里补一个分支。
func BalanceTargetTypes() []string {
	return []string{"deepseek"}
}

// IsValidBalanceTargetType 校验供应商类型是否受支持。
func IsValidBalanceTargetType(s string) bool {
	for _, valid := range BalanceTargetTypes() {
		if s == valid {
			return true
		}
	}
	return false
}

// MaskAPIKey 把密钥缩成可辨认的掩码。
//
// 保留头尾各 4 个字符：头几位能区分是哪个供应商的 key（sk- 之类），
// 尾部能让人确认「是这把，不是上个月那把」。中间一律替换，
// 长度也统一成固定值，避免从长度反推真实密钥。
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) <= 8 {
		// 太短就不露出任何字符：露 4 个等于露一半。
		return "********"
	}
	return string(r[:4]) + "********" + string(r[len(r)-4:])
}
