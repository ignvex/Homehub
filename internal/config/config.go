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
// 可配置多个命名代理，供需要外网访问的服务引用。
// Type 支持 "http"（默认）和 "socks5"；Enabled 控制该条代理是否生效。
type ProxyEntry struct {
	Name     string `json:"name"`      // 代理名称（唯一标识）
	Type     string `json:"type"`      // 代理类型：http / socks5，默认 "http"
	HTTPURL  string `json:"http_url"`  // HTTP 代理地址（如 http://127.0.0.1:7890）
	HTTPSURL string `json:"https_url"` // HTTPS 代理地址（type=http 时用于 https 请求；type=socks5 不使用此字段）
	Enabled  bool   `json:"enabled"`   // 是否启用该代理
}

// ProxyConfig 是全局代理配置。
//
// 与旧版按模块重复声明 proxy_names 不同，这里只有一份默认值：
// 所有出网模块（RSS 抓取、图片缓存、Gravatar、favicon）都用 Default
// 指向的代理链，不再各自配置。这样新增一个出网模块时不会因为漏配
// 而静默走直连——那类失败表现为超时，排查时很难想到是配置缺失。
//
// Default 为空表示全局直连。
type ProxyConfig struct {
	Entries []ProxyEntry `json:"entries"` // 命名代理池
	Default []string     `json:"default"` // 默认代理链（按优先级），空=直连
}

// GravatarConfig Gravatar 头像配置。
type GravatarConfig struct {
	Enabled      bool   `json:"enabled"`       // 是否启用以 Gravatar 生成头像
	DefaultStyle string `json:"default_style"` // 默认头像风格：identicon / mp / robohash / retro / wavatar / blank / 404
	DefaultSize  int    `json:"default_size"`  // 头像缓存尺寸（像素），默认 256，范围 16-512
	Rating       string `json:"rating"`        // 内容分级：g / pg / r / x
	// TimeoutSeconds 单次上游请求超时（秒），默认 10。
	TimeoutSeconds int `json:"timeout_seconds"`
	// MaxBytes 单张头像的大小上限（字节），默认 2MB。
	// 超过上限的响应直接丢弃，不截断写入缓存。
	MaxBytes int64 `json:"max_bytes"`
}

// BookmarkConfig 书签模块配置。
//
// favicon 抓取使用全局默认代理（ProxyConfig.Default），
// 因此这里不再单独声明 proxy_names。
type BookmarkConfig struct{}

// RSSConfig RSS 订阅模块配置。
type RSSConfig struct {
	// RetentionUnlimited 关闭条目数量上限，默认开启。
	//
	// 开启时不执行任何清理，旧条目一直保留；关闭后由 RetentionCount
	// 决定每个源保留多少条。默认选无限是因为清理不可逆：源站过期后
	// 条目再也抓不回来，而磁盘成本远低于丢掉历史。
	RetentionUnlimited bool `json:"retention_unlimited"`
	// RetentionCount 是关闭无限存储时，每个订阅源保留的条目数，默认 5000。
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

// ExtensionConfig 是 HomeHub 与 HomeHub Extension 的唯一连接配置。
//
// 合并了旧版的三个入口：`extension_hub`（签名通道）、
// `rss.extension_url`（代抓）、`gravatar.extension_url`（头像代理）。
// 它们指向同一个服务，本该共用同一份地址与同一套认证：旧版让它们各自
// 持有 Bearer token，结果是同一个对端存在两套凭据、三处地址配置，
// 改一个域名要改三个地方。现在只留这一份。
//
// 认证只有 Ed25519 请求签名一种。`/hub/*`、`/fetch`、`/avatar` 共用同一把密钥。
type ExtensionConfig struct {
	// Enabled 是否启用对接。默认 false。
	//
	// 关闭时：RSS 完全走本地抓取，头像直连 Gravatar，签名通道不可用。
	Enabled bool `json:"enabled"`

	// BaseURL 是 Extension 的 API 根，必须包含版本前缀，例如
	// `https://ext.example.com/api/v1`。端点路径会拼在它后面。
	BaseURL string `json:"base_url"`

	// PrivateKeyPEM 是 Ed25519 私钥的 PKCS#8 PEM 原文。
	//
	// 写进 config.json 而不是单独的文件，也不是数据库：config.json 本身
	// 已经是最高敏感度的文件（0600、已 gitignore、备份清单里必有它），
	// 再拆一个 pem 文件只增加运维负担，安全性没有任何提升。
	// 公钥由使用方（Extension 侧）存在自己的 config.json 里。
	PrivateKeyPEM string `json:"private_key_pem"`

	// Features 按能力细分是否交给 Extension。
	//
	// 一个 enabled 开关同时控制三条通路会出问题：Extension 目前只实现了
	// /hub/*，/fetch 与 /avatar 还是空的。若共用同一个开关，打开签名通道
	// 的瞬间头像与抓取会一起 404——而那两条通路本来工作得好好的。
	//
	// 连接与认证仍然只有一份（就是这个段），能力则各自声明。
	Features ExtensionFeatures `json:"features"`

	// ProxyNames 是这个连接专用的代理链，**默认空 = 直连**。
	//
	// 与全局 ProxyConfig.Default 的关系：Extension 通常是内网或同机部署，
	// 走代理反而绕远路、还可能因为代理不转发内网地址而失败。所以这里
	// 不开继承全局默认——需要走代理时显式填写。
	ProxyNames []string `json:"proxy_names"`

	// TimeoutSeconds 单次 hub 请求超时（秒），默认 30。
	// 同步抓取端点 /refresh 另有 2 分钟的固定预算，不受这里约束。
	TimeoutSeconds int `json:"timeout_seconds"`
}

// ExtensionFeatures 声明哪些能力交给 Extension。
//
// 缺省全部 false：升级后行为与升级前一致，不会因为新增一个配置段
// 就把流量悄悄改道。
type ExtensionFeatures struct {
	// Hub 走 /api/v1/hub/* 签名通道（订阅镜像、访问密钥）。
	Hub bool `json:"hub"`
	// Fetch 把 RSS 抓取交给 GET /fetch。
	Fetch bool `json:"fetch"`
	// Avatar 把头像下载交给 GET /avatar/{hash}。
	Avatar bool `json:"avatar"`
}

// Config 应用顶层配置，对应 config.json 文件结构。
type Config struct {
	Server         ServerConfig         `json:"server"`
	Database       DatabaseConfig       `json:"database"`
	Auth           AuthConfig           `json:"auth"`
	Log            LogConfig            `json:"log"`
	WebUI          WebUIConfig          `json:"webui"`
	Proxy          ProxyConfig          `json:"proxy"`
	Gravatar       GravatarConfig       `json:"gravatar"`
	Bookmark       BookmarkConfig       `json:"bookmark"`
	RSS            RSSConfig            `json:"rss"`
	BalanceMonitor BalanceMonitorConfig `json:"balance_monitor"`
	Pastebin       PastebinConfig       `json:"pastebin"`
	Extension      ExtensionConfig      `json:"extension"`
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
		for _, p := range cfg.Proxy.Entries {
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
		// 不写「配置校验失败」前缀：validate 自己已经带了，再包一层
		// 会得到「配置校验失败：配置校验失败：…」。用户需要的额外
		// 信息只有一句——文件没被动过。
		return fmt.Errorf("%w（配置未写入，文件保持原样）", err)
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
