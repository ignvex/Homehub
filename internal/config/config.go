// Package config 提供 HomeHub 应用配置的加载、热更新与并发安全读取。
//
// 配置以 JSON 单文件形式存储（默认 config.json），启动时加载，
// 运行时通过 fsnotify 监听文件变更自动热重载。
// 读取操作通过 atomic.Pointer 实现无锁访问，适合高频调用场景。
package config

import (
	"sync"
	"sync/atomic"
)

// ServerConfig 服务器监听配置。
type ServerConfig struct {
	Host string `json:"host"` // 监听地址，默认 "0.0.0.0"
	Port int    `json:"port"` // 监听端口，默认 8080
}

// DatabaseConfig 数据库连接配置。
type DatabaseConfig struct {
	Host     string `json:"host"`     // 数据库主机地址
	Port     int    `json:"port"`     // 数据库端口，默认 5432
	User     string `json:"user"`     // 数据库用户名
	Password string `json:"password"` // 数据库密码（明文存储，仅限个人单机场景）
	DBName   string `json:"dbname"`   // 数据库名称
	SSLMode  string `json:"sslmode"`  // SSL 模式，默认 "disable"
}

// AuthConfig 用户认证配置。
type AuthConfig struct {
	Username      string `json:"username"`       // 登录用户名，默认 "admin"
	Password      string `json:"password"`       // 初始密码（首次启动创建用户后不再使用）
	SessionSecret string `json:"session_secret"` // Cookie 会话加密密钥，长度需 ≥ 32
}

// BackgroundConfig 页面背景配置。
//
// 控制首页背景的展示模式与素材选择。
type BackgroundConfig struct {
	Mode             string   `json:"mode"`               // 背景模式：single / carousel / random，默认 "single"
	IntervalSeconds  int      `json:"interval_seconds"`   // 轮播间隔（秒），默认 30
	TransitionEffect string   `json:"transition_effect"`  // 过渡效果：fade / slide，默认 "fade"
	FadeDurationMs   int      `json:"fade_duration_ms"`   // 淡入淡出时长（毫秒），默认 1000
	SelectedAssetIDs []uint64 `json:"selected_asset_ids"` // 选中的媒体资源 ID 列表
}

// HomeWebUIConfig 首页展示配置。
type HomeWebUIConfig struct {
	Background BackgroundConfig `json:"background"`
}

// LoginWebUIConfig 登录页展示配置。
type LoginWebUIConfig struct {
	Background BackgroundConfig `json:"background"`
	Layout     string           `json:"layout"` // left / center / right
}

// WebUIConfig Web 前端页面配置。
type WebUIConfig struct {
	Home  HomeWebUIConfig  `json:"home"`
	Login LoginWebUIConfig `json:"login"`
}

// LogConfig 日志配置。
type LogConfig struct {
	Level    string `json:"level"`     // 日志级别：debug / info / warn / error，默认 "info"
	Format   string `json:"format"`    // 日志格式：json / colored，默认 "json"
	DBWriter bool   `json:"db_writer"` // 是否将日志写入数据库，默认 true
}

// ProxyEntry 代理服务器配置条目。
//
// 可配置多个命名代理，供 Gravatar / Bookmark / RSS 等需要外网访问的服务引用。
// Type 支持 "http"（默认）和 "socks5"；Enabled 控制该条代理是否生效。
type ProxyEntry struct {
	Name     string `json:"name"`      // 代理名称（唯一标识）
	Type     string `json:"type"`      // 代理类型：http / socks5，默认 "http"
	HTTPURL  string `json:"http_url"`  // HTTP 代理地址（如 http://127.0.0.1:7890）
	HTTPSURL string `json:"https_url"` // HTTPS 代理地址（type=http 时用于 https 请求；type=socks5 不使用此字段）
	Enabled  bool   `json:"enabled"`   // 是否启用该代理
}

// ProxyServiceConfig 各服务引用代理的通用配置段。
//
// UseProxy 为服务级代理开关，ProxyNames 为按优先级排列的代理名称列表。
// 抓取时按顺序尝试代理，全失败则退回到直连。
type ProxyServiceConfig struct {
	UseProxy   bool     `json:"use_proxy"`   // 是否启用代理
	ProxyNames []string `json:"proxy_names"` // 代理名称列表（按优先级排列，fallback 链）
}

// GravatarConfig Gravatar 头像配置。
type GravatarConfig struct {
	Enabled        bool   `json:"enabled"`          // 是否启用以 Gravatar 生成头像
	DefaultStyle   string `json:"default_style"`    // 默认头像风格：identicon / mp / robohash / retro / wavatar / blank / 404
	DefaultSize    int    `json:"default_size"`     // 头像尺寸（像素），默认 200
	Rating         string `json:"rating"`           // 内容分级：g / pg / r / x
	UseProxy       bool   `json:"use_proxy"`        // 是否通过代理下载 Gravatar
	ProxyName      string `json:"proxy_name"`       // 引用的代理名称（对应 proxy 列表中的 name）
	CustomProxyURL string `json:"custom_proxy_url"` // 自定义代理 URL（优先级高于 proxy_name）
}

// BookmarkConfig 书签模块配置。
type BookmarkConfig struct {
	ProxyServiceConfig
}

// RSSConfig RSS 订阅模块配置。
type RSSConfig struct {
	ProxyServiceConfig
	RetentionCount          int  `json:"retention_count"`           // 每个订阅源保留条目数，默认 5000
	BackgroundFetchEnabled  bool `json:"background_fetch_enabled"`  // 是否启用后台定时抓取，默认 true
	BackgroundFetchInterval int  `json:"background_fetch_interval"` // 后台抓取间隔（秒），默认 300
}

// BilibiliConfig B 站爬虫完整配置。
//
// 开关是 config.json 的唯一来源，crawler_state 表仅存储运行时进度。
type BilibiliConfig struct {
	// === 开关（唯一来源，不等同于 crawler_state.Enabled） ===
	Enabled        bool `json:"enabled"`         // 总开关
	VideoEnabled   bool `json:"video_enabled"`   // 视频子开关
	ArticleEnabled bool `json:"article_enabled"` // 专栏子开关
	UserEnabled    bool `json:"user_enabled"`    // 用户子开关

	// === 认证 ===
	SESSDATA   string `json:"sessdata"`     // Cookie SESSDATA（会话令牌）
	BiliJCT    string `json:"bili_jct"`     // Cookie bili_jct（CSRF Token）
	DedeUserID string `json:"dede_user_id"` // Cookie DedeUserID（用户 UID）
	UserAgent  string `json:"user_agent"`   // 自定义 UA（空则用默认 Chrome 桌面端）

	// === 网络 ===
	ProxyURL string   `json:"proxy_url"` // 代理地址（空=直连），不复用全局 proxy 列表
	UAPool   []string `json:"ua_pool"`   // UA 轮换池

	// === 全局速率 ===
	RateMs          int `json:"rate_ms"`           // 请求间隔（毫秒），默认 2000
	ActiveStartHour int `json:"active_start_hour"` // 活跃时段起点，默认 0
	ActiveEndHour   int `json:"active_end_hour"`   // 活跃时段终点，默认 6

	// === 生命周期 ===
	Lifecycle LifecycleConfig `json:"lifecycle"`

	// === 发现策略 ===
	Discovery DiscoveryConfig `json:"discovery"`

	// === 快照策略 ===
	Snapshot SnapshotConfig `json:"snapshot"`
}

// LifecycleConfig 内容生命周期策略配置。
type LifecycleConfig struct {
	NewbornMaxHours         int `json:"newborn_max_hours"`          // newborn 阶段最大时长（h），默认 2
	NewbornUpdateIntervalM  int `json:"newborn_update_interval_m"`  // newborn 更新间隔（min），默认 30
	GrowingMaxHours         int `json:"growing_max_hours"`          // growing 阶段最大时长（h），默认 24
	GrowingUpdateIntervalM  int `json:"growing_update_interval_m"`  // growing 更新间隔（min），默认 120
	MatureMaxDays           int `json:"mature_max_days"`            // mature 阶段最大天数，默认 7
	MatureUpdateIntervalM   int `json:"mature_update_interval_m"`   // mature 更新间隔（min），默认 720
	ArchivedUpdateIntervalM int `json:"archived_update_interval_m"` // archived 更新间隔（min），默认 10080
}

// DiscoveryConfig 发现策略配置。
type DiscoveryConfig struct {
	IntervalHours       int  `json:"interval_hours"`         // 发现轮询间隔（h），默认 12
	PopularLimit        int  `json:"popular_limit"`          // 热门榜条数，默认 50
	RankingRidsPerRound int  `json:"ranking_rids_per_round"` // 每轮查询几个分区排行榜，默认 5
	RankingLimit        int  `json:"ranking_limit"`          // 每个分区排行榜条数，默认 100
	DynamicEnabled      bool `json:"dynamic_enabled"`        // 是否启用动态流入口
}

// SnapshotConfig 快照策略配置。
type SnapshotConfig struct {
	MinIntervalM       int     `json:"min_interval_m"`       // 快照最小间隔（min），默认 15
	MinChangePct       float64 `json:"min_change_pct"`       // 触发快照的最小变化百分比，默认 0.05
	MaxPerEntity       int     `json:"max_per_entity"`       // 每个实体最多保留快照数，默认 30
	PurgeIntervalHours int     `json:"purge_interval_hours"` // 清理间隔（h），默认 24
}

// Config 应用顶层配置，对应 config.json 文件结构。
type Config struct {
	Server       ServerConfig     `json:"server"`
	Database     DatabaseConfig   `json:"database"`
	Auth         AuthConfig       `json:"auth"`
	Log          LogConfig        `json:"log"`
	Background   BackgroundConfig `json:"background"` // 兼容旧版配置
	WebUI        WebUIConfig      `json:"webui"`
	ProxyEnabled bool             `json:"proxy_enabled"` // 全局代理总开关（关闭时所有代理均无效）
	Proxy        []ProxyEntry     `json:"proxy,omitempty"`
	Gravatar     GravatarConfig   `json:"gravatar"`
	Bookmark     BookmarkConfig   `json:"bookmark"`
	RSS          RSSConfig        `json:"rss"`
	Bilibili     BilibiliConfig   `json:"bilibili"`
}

var (
	globalConfig atomic.Pointer[Config] // 当前生效的配置（无锁读取）
	_            sync.RWMutex           // 保护热加载期间的临界区（预留）
)

// Get 返回当前生效的配置指针。
//
// 通过 atomic.Pointer 实现零锁读取，并发安全。
// 调用方不得修改返回的 Config 实例。
// 若配置尚未加载，返回 nil。
func Get() *Config {
	return globalConfig.Load()
}

// atomicStore 原子替换全局配置。
func atomicStore(cfg *Config) {
	globalConfig.Store(cfg)
}

// ResolveProxy 通用代理解析，支持多代理 fallback 链。
//
// 参数：
//   - serviceCfg：服务自身的代理配置（含 UseProxy 和 ProxyNames）
//   - customURL：自定义代理 URL（优先级最高，如 Gravatar 的 custom_proxy_url）
//
// 返回代理地址列表（按优先级排列），空切片表示直连。
// 调用方按顺序尝试每个代理，失败则尝试下一个。
func ResolveProxy(serviceCfg ProxyServiceConfig, customURL string) []string {
	cfg := Get()
	if cfg == nil {
		return nil
	}

	// 最高优先级：自定义代理 URL
	if customURL != "" {
		return []string{customURL}
	}

	// 全局开关关闭 → 直连
	if !cfg.ProxyEnabled {
		return nil
	}

	// 服务未启用代理 → 直连
	if !serviceCfg.UseProxy || len(serviceCfg.ProxyNames) == 0 {
		return nil
	}

	// 按 ProxyNames 顺序收集可用的代理地址
	result := make([]string, 0, len(serviceCfg.ProxyNames))
	for _, name := range serviceCfg.ProxyNames {
		for _, p := range cfg.Proxy {
			if p.Name == name && p.Enabled {
				addr := p.HTTPURL
				if addr == "" {
					continue
				}
				result = append(result, addr)
				break
			}
		}
	}

	return result
}

// ListEnabledProxies 返回当前所有启用的代理列表。
//
// 供 API 端点在 /config 响应中返回可用代理列表，
// 前端可根据此列表选择代理。
func ListEnabledProxies() []ProxyEntry {
	cfg := Get()
	if cfg == nil || !cfg.ProxyEnabled {
		return nil
	}

	result := make([]ProxyEntry, 0)
	for _, p := range cfg.Proxy {
		if p.Enabled {
			result = append(result, p)
		}
	}
	return result
}
