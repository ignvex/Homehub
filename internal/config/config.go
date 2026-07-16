// Package config 提供 HomeHub 应用配置的加载、热更新与并发安全读取。
//
// 配置以 JSON 单文件形式存储（默认 config.json），启动时加载，
// 运行时通过 fsnotify 监听文件变更自动热重载。
// 读取操作通过 atomic.Pointer 实现无锁访问，适合高频调用场景。
package config

import (
	"encoding/json"
	"fmt"
	"os"
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
// ProxyNames 为按优先级排列的代理名称列表，引用 proxy 池中的代理。
// 空列表表示直连，多个名称形成 fallback 链。
type ProxyServiceConfig struct {
	ProxyNames []string `json:"proxy_names"` // 代理名称列表（按优先级排列，fallback 链），空=直连
}

// GravatarConfig Gravatar 头像配置。
type GravatarConfig struct {
	Enabled      bool     `json:"enabled"`       // 是否启用以 Gravatar 生成头像
	DefaultStyle string   `json:"default_style"` // 默认头像风格：identicon / mp / robohash / retro / wavatar / blank / 404
	DefaultSize  int      `json:"default_size"`  // 头像尺寸（像素），默认 200
	Rating       string   `json:"rating"`        // 内容分级：g / pg / r / x
	ProxyNames   []string `json:"proxy_names"`   // 代理名称列表（引用 proxy 池），空=直连
}

// BookmarkConfig 书签模块配置。
type BookmarkConfig struct {
	ProxyServiceConfig
}

// RSSConfig RSS 订阅模块配置。
type RSSConfig struct {
	ProxyServiceConfig
	RetentionCount          int    `json:"retention_count"`           // 每个订阅源保留条目数，默认 5000
	BackgroundFetchEnabled  bool   `json:"background_fetch_enabled"`  // 是否启用后台定时抓取，默认 true
	BackgroundFetchInterval int    `json:"background_fetch_interval"` // 后台抓取间隔（秒），默认 300
	UserAgent               string `json:"user_agent"`                // 自定义 User-Agent，空则默认 Chrome 150
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

	// === 调试 ===
	Debug bool `json:"debug"` // 调试模式：开启后无视工作时间限制，全天候运行

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
	IntervalHours        int  `json:"interval_hours"`         // 发现轮询间隔（h），默认 12
	PopularLimit         int  `json:"popular_limit"`          // 热门榜条数，默认 50
	RankingRidsPerRound  int  `json:"ranking_rids_per_round"` // 每轮查询几个分区排行榜，默认 5
	RankingLimit         int  `json:"ranking_limit"`          // 每个分区排行榜条数，默认 100
	DynamicEnabled       bool `json:"dynamic_enabled"`        // 是否启用动态流入口
	WeeklyEnabled        bool `json:"weekly_enabled"`         // 每周必看入口
	WeeklySeriesCount    int  `json:"weekly_series_count"`    // 每周必看取最近几期，默认 3
	PreciousEnabled      bool `json:"precious_enabled"`       // 入站必刷入口
	UserContentDiscovery bool `json:"user_content_discovery"` // 发现 UP 主后自动拉取其全部投稿
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
	Server         ServerConfig         `json:"server"`
	Database       DatabaseConfig       `json:"database"`
	Auth           AuthConfig           `json:"auth"`
	Log            LogConfig            `json:"log"`
	WebUI          WebUIConfig          `json:"webui"`
	Proxy          []ProxyEntry         `json:"proxy,omitempty"`
	Gravatar       GravatarConfig       `json:"gravatar"`
	Bookmark       BookmarkConfig       `json:"bookmark"`
	RSS            RSSConfig            `json:"rss"`
	Bilibili       BilibiliConfig       `json:"bilibili"`
	BalanceMonitor BalanceMonitorConfig `json:"balance_monitor"`
	Pastebin       PastebinConfig       `json:"pastebin"`
}

// BalanceTarget 单个余额监控点配置。
type BalanceTarget struct {
	Name    string `json:"name"`     // 监控点名称，如 "DeepSeek 主号"
	Type    string `json:"type"`     // 类型：目前仅支持 deepseek
	BaseURL string `json:"base_url"` // API 基础地址，如 https://api.deepseek.com
	APIKey  string `json:"api_key"`  // API 密钥
	Enabled bool   `json:"enabled"`  // 是否启用该监控点
}

// BalanceMonitorConfig 余额监控模块配置。
type BalanceMonitorConfig struct {
	Enabled                bool            `json:"enabled"`                  // 模块总开关
	RefreshIntervalSeconds int             `json:"refresh_interval_seconds"` // 后台刷新间隔（秒），默认 60
	Targets                []BalanceTarget `json:"targets"`                  // 监控点列表
}

// PastebinConfig 剪贴板模块配置。
type PastebinConfig struct {
	Enabled              bool `json:"enabled"`                // 模块总开关
	MaxContentLength     int  `json:"max_content_length"`     // 单条内容最大字节数，默认 1048576 (1MB)
	DefaultTTLHours      int  `json:"default_ttl_hours"`      // 默认过期时间（小时），默认 168 (7天)
	CleanupIntervalHours int  `json:"cleanup_interval_hours"` // 过期清理间隔（小时），默认 24
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

// ResolveProxy 从代理池中按名称查找可用的代理条目。
//
// proxyNames 为按优先级排列的代理名称列表，空列表或未匹配到任何代理时返回 nil。
// 返回的 ProxyEntry 包含 type 字段（http/socks5），调用方据此选择连接方式。
func ResolveProxy(proxyNames []string) []ProxyEntry {
	if len(proxyNames) == 0 {
		return nil
	}

	cfg := Get()
	if cfg == nil {
		return nil
	}

	result := make([]ProxyEntry, 0, len(proxyNames))
	for _, name := range proxyNames {
		for _, p := range cfg.Proxy {
			if p.Name == name && p.Enabled {
				result = append(result, p)
				break
			}
		}
	}

	return result
}

// ListEnabledProxies 返回当前所有启用的代理列表。
func ListEnabledProxies() []ProxyEntry {
	cfg := Get()
	if cfg == nil {
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

// AtomicJSONUpdate 以保留非结构体字段（如 _comment）的方式更新 config.json。
//
// 读入原始 JSON 为 map，调用 fn 修改 map，写入文件后触发热重载。
// 与直接序列化 Config 结构体的方式不同，本函数保留所有未映射到结构体的 JSON 字段。
func AtomicJSONUpdate(path string, fn func(raw map[string]any) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	if err := fn(raw); err != nil {
		return err
	}

	out, err := json.MarshalIndent(raw, "", "    ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return Load(path)
}
