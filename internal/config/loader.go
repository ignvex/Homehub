package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
)

// 哨兵错误：首次启动时 config.json 不存在，已生成默认配置文件。
var ErrConfigGenerated = errors.New("默认配置文件已生成，请编辑后重新启动")

// loadedPath 记录当前生效的配置文件路径（由 Load 设置）。
//
// 供需要回写配置的模块使用，保证 HOMEHUB_CONFIG 指向自定义路径时
// Web 端配置更新与密码清除写入的是同一个文件。
var loadedPath atomic.Value

// Path 返回当前生效的配置文件路径；尚未加载时返回 "config.json"。
func Path() string {
	if v, ok := loadedPath.Load().(string); ok && v != "" {
		return v
	}
	return "config.json"
}

// 允许的日志级别枚举。
var validLogLevels = []string{"debug", "info", "warn", "error"}

// 允许的日志格式枚举。
var validLogFormats = []string{"json", "colored"}

// 允许的背景模式枚举。
var validBackgroundModes = []string{"single", "carousel", "random"}

// 允许的背景过渡效果枚举。
var validBackgroundTransitions = []string{"fade", "slide"}

// Load 从 JSON 文件加载配置，填充默认值，校验，并原子替换全局配置。
//
// 若文件不存在，生成带默认值的 config.json 并返回 ErrConfigGenerated。
// 调用方应据此退出，提示用户编辑配置文件后重启。
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createDefaultConfig(path)
		}
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}

	applyDefaults(&cfg)
	applyExplicitBoolOverrides(data, &cfg)

	if err := validate(&cfg); err != nil {
		return err
	}

	loadedPath.Store(path)
	atomicStore(&cfg)
	return nil
}

// createDefaultConfig 写入带默认值的配置文件并返回 ErrConfigGenerated。
func createDefaultConfig(path string) error {
	cfg := Config{}
	applyDefaults(&cfg)
	// 无原始 JSON：所有开关取 fallback（模块默认启用）
	applyBoolDefaults(nil, &cfg)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建默认配置文件 %s 失败: %w", path, err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("写入默认配置文件 %s 失败: %w", path, err)
	}

	return fmt.Errorf("%w: %s", ErrConfigGenerated, path)
}

// applyDefaults 对未设置（零值）的字段填入合理默认值。
//
// 注意：Go 标准库 json 不支持 default tag，因此显式处理。
func applyDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}

	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Database.User == "" {
		cfg.Database.User = "homehub"
	}
	if cfg.Database.DBName == "" {
		cfg.Database.DBName = "homehub"
	}

	if cfg.Auth.Username == "" {
		cfg.Auth.Username = "admin"
	}

	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}

	applyBackgroundDefaults(&cfg.WebUI.Home.Background)
	applyBackgroundDefaults(&cfg.WebUI.Login.Background)
	if cfg.WebUI.Login.Layout == "" {
		cfg.WebUI.Login.Layout = "center"
	}

	if cfg.Gravatar.DefaultStyle == "" {
		cfg.Gravatar.DefaultStyle = "identicon"
	}
	if cfg.Gravatar.DefaultSize == 0 {
		cfg.Gravatar.DefaultSize = 256
	}
	if cfg.Gravatar.Rating == "" {
		cfg.Gravatar.Rating = "g"
	}
	if cfg.Gravatar.TimeoutSeconds == 0 {
		cfg.Gravatar.TimeoutSeconds = 10
	}
	if cfg.Gravatar.MaxBytes == 0 {
		cfg.Gravatar.MaxBytes = 2 * 1024 * 1024 // 2MB
	}

	for i := range cfg.Proxy {
		if cfg.Proxy[i].Type == "" {
			cfg.Proxy[i].Type = "http"
		}
	}

	// RSS 抓取参数默认值
	if cfg.RSS.FetchConcurrency == 0 {
		cfg.RSS.FetchConcurrency = 4
	}
	if cfg.RSS.FetchTimeoutSeconds == 0 {
		cfg.RSS.FetchTimeoutSeconds = 30
	}
	if cfg.RSS.ImageCacheConcurrency == 0 {
		cfg.RSS.ImageCacheConcurrency = 6
	}
	if cfg.RSS.ImageMaxBytes == 0 {
		cfg.RSS.ImageMaxBytes = 10 * 1024 * 1024 // 10MB
	}
	if cfg.RSS.ImageRetentionDays == 0 {
		cfg.RSS.ImageRetentionDays = 30
	}
	if cfg.RSS.ImageCleanupIntervalHours == 0 {
		cfg.RSS.ImageCleanupIntervalHours = 24
	}

	// Balance Monitor 默认值
	if cfg.BalanceMonitor.RefreshIntervalSeconds == 0 {
		cfg.BalanceMonitor.RefreshIntervalSeconds = 60
	}
	for i := range cfg.BalanceMonitor.Targets {
		if cfg.BalanceMonitor.Targets[i].Type == "" {
			cfg.BalanceMonitor.Targets[i].Type = "oneapi"
		}
	}

	// Pastebin 默认值
	if cfg.Pastebin.MaxContentLength == 0 {
		cfg.Pastebin.MaxContentLength = 1048576 // 1MB
	}
	if cfg.Pastebin.DefaultTTLHours == 0 {
		cfg.Pastebin.DefaultTTLHours = 168 // 7天
	}
	if cfg.Pastebin.CleanupIntervalHours == 0 {
		cfg.Pastebin.CleanupIntervalHours = 24
	}
}

// applyMissingBoolDefaults 为"缺省即启用"的布尔开关补默认值。
//
// 这几个模块的 enabled 字段若在 JSON 中缺失，反序列化会得到 false，
// 表现为功能静默不可用——比"关不掉"更难排查。因此显式区分
// “键不存在”与“显式写 false”：前者补 true，后者尊重用户选择。
// boolFlag 描述一个"缺省有默认值"的布尔开关。
//
// bool 零值永远是 false，无法区分"字段缺失"与"显式写 false"。
// 不处理就会导致：配置里漏写一项，功能静默关闭 —— 比"关不掉"更难排查。
type boolFlag struct {
	section  string // 顶层段名
	field    string // 段内键名
	target   *bool
	fallback bool // 键缺失时的取值
}

// boolFlags 汇总所有需要区分"缺失"与"显式 false"的开关。
//
// 加新开关时在这里登记一行，而不是在 applyDefaults 里写赋值 ——
// 后者对显式 false 无效（会被覆盖）。
func (cfg *Config) boolFlags() []boolFlag {
	return []boolFlag{
		{"gravatar", "enabled", &cfg.Gravatar.Enabled, true},
		{"pastebin", "enabled", &cfg.Pastebin.Enabled, true},
		{"rss", "background_fetch_enabled", &cfg.RSS.BackgroundFetchEnabled, true},
		{"rss", "image_cache_enabled", &cfg.RSS.ImageCacheEnabled, true},
		{"rss", "image_cleanup_enabled", &cfg.RSS.ImageCleanupEnabled, true},
		// 下面两项默认关闭：开启会额外消耗资源，交给用户主动选择。
		{"balance_monitor", "enabled", &cfg.BalanceMonitor.Enabled, false},
		{"log", "db_writer", &cfg.Log.DBWriter, false},
	}
}

// applyBoolDefaults 为缺失的布尔开关填入默认值。
//
// raw 为 nil 表示没有原始 JSON（新建配置文件），全部使用 fallback；
// 否则按"键存在则尊重显式值，缺失则用 fallback"处理。
func applyBoolDefaults(raw map[string]json.RawMessage, cfg *Config) {
	for _, f := range cfg.boolFlags() {
		*f.target = f.fallback
		if raw == nil {
			continue
		}

		body, ok := raw[f.section]
		if !ok {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			continue
		}
		rawVal, ok := fields[f.field]
		if !ok {
			continue
		}
		var v bool
		if err := json.Unmarshal(rawVal, &v); err != nil {
			continue
		}
		*f.target = v
	}
}

// applyExplicitBoolOverrides 从原始 JSON 恢复显式设置的布尔开关。
func applyExplicitBoolOverrides(data []byte, cfg *Config) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return // 已通过结构体解析，这里失败说明数据异常，交给后续校验
	}
	applyBoolDefaults(raw, cfg)
}

func applyBackgroundDefaults(background *BackgroundConfig) {
	if background.Mode == "" {
		background.Mode = "single"
	}
	if background.IntervalSeconds == 0 {
		background.IntervalSeconds = 30
	}
	if background.TransitionEffect == "" {
		background.TransitionEffect = "fade"
	}
	if background.FadeDurationMs == 0 {
		background.FadeDurationMs = 1000
	}
}

// validate 校验配置的必填字段和取值范围。
//
// 返回聚合错误，一次性报告所有不合法项。
func validate(cfg *Config) error {
	var errs []string

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, "server.port 必须在 1-65535 之间")
	}

	if cfg.Database.Host == "" {
		errs = append(errs, "database.host 不能为空")
	}
	if cfg.Database.Port < 1 || cfg.Database.Port > 65535 {
		errs = append(errs, "database.port 必须在 1-65535 之间")
	}
	if cfg.Database.User == "" {
		errs = append(errs, "database.user 不能为空")
	}
	if cfg.Database.DBName == "" {
		errs = append(errs, "database.dbname 不能为空")
	}

	if cfg.Auth.SessionSecret == "" {
		errs = append(errs, "auth.session_secret 不能为空，请用 openssl rand -hex 32 生成后填入")
	} else if len(cfg.Auth.SessionSecret) < 32 {
		errs = append(errs, "auth.session_secret 长度不能少于 32 字符")
	}

	if !slices.Contains(validLogLevels, strings.ToLower(cfg.Log.Level)) {
		errs = append(errs, "log.level 必须为 debug/info/warn/error 之一")
	}
	if !slices.Contains(validLogFormats, strings.ToLower(cfg.Log.Format)) {
		errs = append(errs, "log.format 必须为 json/colored 之一")
	}

	validateBackground := func(path string, background BackgroundConfig) {
		if !slices.Contains(validBackgroundModes, strings.ToLower(background.Mode)) {
			errs = append(errs, path+".mode 必须为 single/carousel/random 之一")
		}
		if background.IntervalSeconds < 1 || background.IntervalSeconds > 3600 {
			errs = append(errs, path+".interval_seconds 必须在 1-3600 之间")
		}
		if !slices.Contains(validBackgroundTransitions, strings.ToLower(background.TransitionEffect)) {
			errs = append(errs, path+".transition_effect 必须为 fade/slide 之一")
		}
		if background.FadeDurationMs < 0 || background.FadeDurationMs > 10000 {
			errs = append(errs, path+".fade_duration_ms 必须在 0-10000 之间")
		}
	}
	validateBackground("webui.home.background", cfg.WebUI.Home.Background)
	validateBackground("webui.login.background", cfg.WebUI.Login.Background)
	if !slices.Contains([]string{"left", "center", "right"}, strings.ToLower(cfg.WebUI.Login.Layout)) {
		errs = append(errs, "webui.login.layout 必须为 left/center/right 之一")
	}

	validGravatarStyles := []string{"identicon", "mp", "robohash", "retro", "wavatar", "blank", "404"}
	if !slices.Contains(validGravatarStyles, strings.ToLower(cfg.Gravatar.DefaultStyle)) {
		errs = append(errs, "gravatar.default_style 必须为 identicon/mp/robohash/retro/wavatar/blank/404 之一")
	}
	// 尺寸上限与 service 的缓存上限保持一致：更大的图只会白白占用数据库。
	if cfg.Gravatar.DefaultSize < 16 || cfg.Gravatar.DefaultSize > 512 {
		errs = append(errs, "gravatar.default_size 必须在 16-512 之间")
	}
	validGravatarRatings := []string{"g", "pg", "r", "x"}
	if !slices.Contains(validGravatarRatings, strings.ToLower(cfg.Gravatar.Rating)) {
		errs = append(errs, "gravatar.rating 必须为 g/pg/r/x 之一")
	}
	if cfg.Gravatar.TimeoutSeconds < 1 || cfg.Gravatar.TimeoutSeconds > 60 {
		errs = append(errs, "gravatar.timeout_seconds 必须在 1-60 之间")
	}
	if cfg.Gravatar.MaxBytes < 64*1024 || cfg.Gravatar.MaxBytes > 10*1024*1024 {
		errs = append(errs, "gravatar.max_bytes 必须在 65536-10485760 (64KB-10MB) 之间")
	}
	if cfg.Gravatar.ExtensionURL != "" {
		u, err := url.Parse(cfg.Gravatar.ExtensionURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, "gravatar.extension_url 必须是完整的 http(s) 地址")
		}
	}

	validProxyTypes := []string{"http", "socks5"}
	for i, p := range cfg.Proxy {
		if !slices.Contains(validProxyTypes, strings.ToLower(p.Type)) {
			errs = append(errs, fmt.Sprintf("proxy[%d].type 必须为 http/socks5 之一", i))
		}
	}

	// Balance Monitor 校验
	validBalanceTypes := []string{"deepseek"}
	if cfg.BalanceMonitor.RefreshIntervalSeconds < 10 || cfg.BalanceMonitor.RefreshIntervalSeconds > 3600 {
		errs = append(errs, "balance_monitor.refresh_interval_seconds 必须在 10-3600 之间")
	}
	for i, t := range cfg.BalanceMonitor.Targets {
		if t.Name == "" {
			errs = append(errs, fmt.Sprintf("balance_monitor.targets[%d].name 不能为空", i))
		}
		if !slices.Contains(validBalanceTypes, strings.ToLower(t.Type)) {
			errs = append(errs, fmt.Sprintf("balance_monitor.targets[%d].type 目前仅支持 deepseek", i))
		}
		if t.BaseURL == "" {
			errs = append(errs, fmt.Sprintf("balance_monitor.targets[%d].base_url 不能为空", i))
		}
		if t.APIKey == "" {
			errs = append(errs, fmt.Sprintf("balance_monitor.targets[%d].api_key 不能为空", i))
		}

	}

	// RSS 抓取参数校验
	if cfg.RSS.FetchConcurrency < 1 || cfg.RSS.FetchConcurrency > 32 {
		errs = append(errs, "rss.fetch_concurrency 必须在 1-32 之间")
	}
	if cfg.RSS.FetchTimeoutSeconds < 5 || cfg.RSS.FetchTimeoutSeconds > 600 {
		errs = append(errs, "rss.fetch_timeout_seconds 必须在 5-600 之间")
	}
	if cfg.RSS.ImageCacheConcurrency < 1 || cfg.RSS.ImageCacheConcurrency > 32 {
		errs = append(errs, "rss.image_cache_concurrency 必须在 1-32 之间")
	}
	if cfg.RSS.ImageMaxBytes < 64*1024 || cfg.RSS.ImageMaxBytes > 50*1024*1024 {
		errs = append(errs, "rss.image_max_bytes 必须在 65536-52428800 (64KB-50MB) 之间")
	}
	if cfg.RSS.ImageRetentionDays < 1 || cfg.RSS.ImageRetentionDays > 3650 {
		errs = append(errs, "rss.image_retention_days 必须在 1-3650 之间")
	}
	if cfg.RSS.ImageCleanupIntervalHours < 1 || cfg.RSS.ImageCleanupIntervalHours > 168 {
		errs = append(errs, "rss.image_cleanup_interval_hours 必须在 1-168 之间")
	}
	if cfg.RSS.ExtensionURL != "" {
		u, err := url.Parse(cfg.RSS.ExtensionURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, "rss.extension_url 必须是完整的 http(s) 地址")
		}
	}

	// Pastebin 校验
	if cfg.Pastebin.MaxContentLength < 1 || cfg.Pastebin.MaxContentLength > 10*1024*1024 {
		errs = append(errs, "pastebin.max_content_length 必须在 1-10485760 (10MB) 之间")
	}
	if cfg.Pastebin.DefaultTTLHours < 1 || cfg.Pastebin.DefaultTTLHours > 8760 {
		errs = append(errs, "pastebin.default_ttl_hours 必须在 1-8760 (1年) 之间")
	}
	if cfg.Pastebin.CleanupIntervalHours < 1 || cfg.Pastebin.CleanupIntervalHours > 168 {
		errs = append(errs, "pastebin.cleanup_interval_hours 必须在 1-168 之间")
	}

	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}
