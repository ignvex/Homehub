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
	DefaultSize  int      `json:"default_size"`  // 头像缓存尺寸（像素），默认 256，范围 16-512
	Rating       string   `json:"rating"`        // 内容分级：g / pg / r / x
	ProxyNames   []string `json:"proxy_names"`   // 直连模式下的代理名称列表（引用 proxy 池），空=直连

	// ExtensionURL 外部图片代理服务地址（如 http://homehub-extension:8090）。
	//
	// 为空时直接从 Gravatar 官方下载（默认）；非空时全部交给 extension：
	// HomeHub 单向发起 GET {ExtensionURL}/avatar/{hash}?s=&d=&r=，
	// extension 不反向连接、不需要数据库凭据。
	ExtensionURL string `json:"extension_url"`
	// ExtensionToken 调用 extension 时的 Bearer Token，留空则不带认证头。
	ExtensionToken string `json:"extension_token"`
	// TimeoutSeconds 单次上游请求超时（秒），默认 10。
	TimeoutSeconds int `json:"timeout_seconds"`
	// MaxBytes 单张头像的大小上限（字节），默认 2MB。
	// 超过上限的响应直接丢弃，不截断写入缓存。
	MaxBytes int64 `json:"max_bytes"`
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

	// FetchConcurrency 同时抓取的订阅源数量，默认 4。
	// 单次抓取变慢时不会拖住整轮：并发上限保证 DB 与网络不至于被打满。
	FetchConcurrency int `json:"fetch_concurrency"`
	// FetchTimeoutSeconds 单个订阅源的抓取超时（秒），默认 30。
	// 超时只影响该订阅源，其他订阅源继续抓取。
	FetchTimeoutSeconds int `json:"fetch_timeout_seconds"`
	// ImageCacheEnabled 是否把条目内嵌图片缓存到本地，默认 true。
	// 关闭后保留原始外链 URL，可显著加快抓取（图片下载是抓取耗时的主要来源）。
	ImageCacheEnabled bool `json:"image_cache_enabled"`
	// ImageCacheConcurrency 图片并发下载数，默认 6。
	ImageCacheConcurrency int `json:"image_cache_concurrency"`
	// ImageMaxBytes 单张图片的大小上限（字节），默认 128MB。
	// 超过上限的图片会被丢弃，而不是截断后写坏文件。
	// 上限放到 2GB 是为高分辨率原图留余量；真正的约束是抓取预算，
	// 单张图太大时会先撞上 fetch_timeout_seconds。
	ImageMaxBytes int64 `json:"image_max_bytes"`
	// ImageCleanupEnabled 是否自动清理没有条目引用的图片缓存，默认 true。
	ImageCleanupEnabled bool `json:"image_cleanup_enabled"`
	// ImageRetentionDays 无引用图片的保留天数，默认 30。
	// 只有超过这个天数仍无人引用的文件才会被删除，给历史条目留出重新抓取登记的时间。
	ImageRetentionDays int `json:"image_retention_days"`
	// ImageCleanupIntervalHours 后台清理间隔（小时），默认 24。
	ImageCleanupIntervalHours int `json:"image_cleanup_interval_hours"`
	// ImageAllowPrivateHosts 是否允许图片缓存访问内网/回环地址，默认 false。
	// 仅在内网 RSS 源确实引用内网图片时开启；开启会削弱 SSRF 防护。
	ImageAllowPrivateHosts bool `json:"image_allow_private_hosts"`

	// ExtensionURL 外部抓取服务地址（如 http://homehub-extension:8090）。
	//
	// 为空时由 HomeHub 自己抓取（默认）。非空时把所有 RSS 抓取交给该服务：
	// HomeHub 单向发起 GET {ExtensionURL}/fetch?url=...，extension 不反向连接。
	ExtensionURL string `json:"extension_url"`
	// ExtensionToken 调用 extension 时的 Bearer Token，留空则不带认证头。
	ExtensionToken string `json:"extension_token"`

	// PikPak 磁力链接辅助功能。关闭时所有相关 API 返回 503，
	// 锁定标记仍然可用（锁定本身不依赖 PikPak）。
	PikPak PikPakConfig `json:"pikpak"`
}

// PikPakConfig 是 PikPak 离线下载集成配置。
//
// 设计边界：HomeHub 只负责“把磁力链接交给 PikPak 并记住结果”，
// 不轮询任务进度、不管理 PikPak 的文件列表。这样即使 PikPak 改了接口，
// 受影响的也只是提交这一个动作。
type PikPakConfig struct {
	// Enabled 是否启用。默认 false —— 涉及第三方账号凭据，必须显式开启。
	Enabled bool `json:"enabled"`
	// Endpoint 服务地址，默认 https://api-drive.mypikpak.com。
	// 允许改是因为 PikPak 有多个区域端点，也可能自建反代。
	Endpoint string `json:"endpoint"`
	// Username / Password 是 PikPak 账号。
	//
	// 与 config.json 里其他凭据一样是明文存储（该文件已 gitignore 且
	// 权限应设为 600）。不用 refresh_token 是因为它过期需要人工续期，
	// 对一个个人项目来说反而更容易失效。
	Username string `json:"username"`
	Password string `json:"password"`
	// ClientID / ClientSecret 是 PikPak 分配的客户端标识。
	// 留空时提交会直接失败并给出明确提示，不猜测默认值。
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// TimeoutSeconds 单次提交的超时，默认 15。
	TimeoutSeconds int `json:"timeout_seconds"`
	// AutoLock 抓取到磁力链接时自动锁定，避免后续抓取把它覆盖掉。
	// 默认 false：自动锁定会让“锁”失去“人工确认”的含义。
	AutoLock bool `json:"auto_lock"`
}

// ExtensionHubConfig 是 HomeHub 作为宿主调用 HomeHub Extension
// `/api/v1/hub/*` 机器通道的配置。
//
// 它与 `rss.extension_url` / `gravatar.extension_url` 是两件不同的事：
// 后两者是 HomeHub 主动调用的无状态端点（`/fetch`、`/avatar`），走 Bearer；
// 这里是 Ed25519 请求签名通道，Extension 侧会持久化订阅与访问密钥。
// 混用一个配置项会让“只代理抓取”和“接管整个阅读器”无法区分。
//
// 默认关闭：未配置时 RSS 完全走本地抓取路径，与 1.0 的行为一致。
type ExtensionHubConfig struct {
	// Enabled 是否启用对接。默认 false。
	Enabled bool `json:"enabled"`
	// BaseURL 是 Extension 的 API 根，必须包含版本前缀，例如
	// `https://ext.example.com/api/v1`。端点路径会拼在它后面。
	BaseURL string `json:"base_url"`
	// PrivateKeyPath 是宿主 Ed25519 私钥（PKCS#8 PEM）的路径。
	// 私钥只在本进程内读取，不写入日志、不入库、不下发到前端。
	PrivateKeyPath string `json:"private_key_path"`
	// TimeoutSeconds 单次请求超时（秒），默认 30。
	// 同步抓取端点 /refresh 另有 2 分钟的固定预算，不受这里约束。
	TimeoutSeconds int `json:"timeout_seconds"`
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
	BalanceMonitor BalanceMonitorConfig `json:"balance_monitor"`
	Pastebin       PastebinConfig       `json:"pastebin"`
	ExtensionHub   ExtensionHubConfig   `json:"extension_hub"`
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

	// 先校验后写盘。顺序反过来的后果是：一次填错字段就会把无效配置
	// 写进文件，而调用方只看到「保存失败」——重启服务就再也起不来，
	// 热重载也会反复报同一个错。校验放在写入之前，坏配置根本落不了地。
	if err := validateCandidate(out); err != nil {
		return fmt.Errorf("配置校验失败，未写入文件: %w", err)
	}

	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return Load(path)
}

// validateCandidate 用与 Load 相同的规则校验待写入的配置字节。
//
// 单独抽出来是为了让「校验」与「生效」可以分开：写入前先跑这一遍，
// 写入后才真正替换全局配置。
func validateCandidate(data []byte) error {
	var candidate Config
	if err := json.Unmarshal(data, &candidate); err != nil {
		return fmt.Errorf("解析失败: %w", err)
	}
	applyDefaults(&candidate)
	applyExplicitBoolOverrides(data, &candidate)
	return validate(&candidate)
}
