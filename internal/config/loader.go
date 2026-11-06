package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ignvex/HomeHub/internal/logger"
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
	// 单文件 bind mount 的宿主机源文件不存在时，Docker 会在宿主机上建一个
	// 同名【目录】挂进来。ReadFile 只会报 "is a directory"，看不出原因；
	// 生成默认配置也会失败。这里给出能直接照做的指引。
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return fmt.Errorf("配置路径 %s 是目录而不是文件。"+
			"在 Docker 中这通常是因为宿主机上的 config.json 挂载前不存在，Docker 自动建了同名目录："+
			"请删除宿主机上的该目录，执行 cp config.example.json config.json 并填写后重新启动", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createDefaultConfig(path)
		}
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	// 配置文件里有数据库密码、session_secret 与签名私钥。权限过宽时
	// 直接收紧到 0600：这不需要征得同意——能读这个文件就能冒充宿主
	// 驱动 Extension，而 chmod 是可逆且无副作用的。
	// 失败只警告不阻断：某些挂载点（如只读 configmap）允许读但不允许改，
	// 那种部署应当由运维在外层保证权限。
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			logger.Warn("配置文件权限过宽且无法收紧，其中含数据库密码与签名私钥",
				"path", path, "mode", info.Mode().Perm().String(), "error", chmodErr)
		} else {
			logger.Info("已把配置文件权限收紧到 0600", "path", path,
				"was", info.Mode().Perm().String())
		}
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

	warnExtensionFetchBudget(&cfg)

	loadedPath.Store(path)
	atomicStore(&cfg)
	return nil
}

// minExtensionFetchBudget 是代抓时建议的最小单源预算。
//
// 本地抓取时 fetch_timeout_seconds 只覆盖「一次 HTTP 往返 + 解析」；
// 交给 Extension 之后，同一次抓取里还叠了宿主导出 URL → Extension
// 取源站 → 宿主解析这么几段，30 秒在源站偏慢时很容易不够。
const minExtensionFetchBudget = 60 * time.Second

// warnExtensionFetchBudget 在启用代抓而预算偏紧时提示一次。
//
// 只警告不修正：把用户的配置悄悄改大，会让「我明明设了 30 秒」变成
// 一个查不出来的谜。这条日志出现在启动时，读者能自己决定要不要调。
func warnExtensionFetchBudget(cfg *Config) {
	if cfg == nil || !cfg.Extension.Enabled || !cfg.Extension.Features.Fetch {
		return
	}
	budget := time.Duration(cfg.RSS.FetchTimeoutSeconds) * time.Second
	if budget >= minExtensionFetchBudget {
		return
	}
	logger.Warn("代抓模式下 rss.fetch_timeout_seconds 偏小，可能频繁超时",
		"current", budget.String(),
		"suggested", minExtensionFetchBudget.String(),
		"hint", "代抓比本地抓取多一层网络往返，30 秒通常不够")
}

// createDefaultConfig 写入带默认值的配置文件并返回 ErrConfigGenerated。
func createDefaultConfig(path string) error {
	cfg := Config{}
	applyDefaults(&cfg)
	// 无原始 JSON：所有开关取 fallback（模块默认启用）
	applyBoolDefaults(nil, &cfg)
	applyZeroableIntDefaults(nil, &cfg)

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
		cfg.Server.Host = DefaultServerHost
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = DefaultServerPort
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

	for i := range cfg.Proxy.Entries {
		if cfg.Proxy.Entries[i].Type == "" {
			cfg.Proxy.Entries[i].Type = "http"
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
		cfg.RSS.ImageMaxBytes = 128 * 1024 * 1024 // 128MB
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

	// Extension 连接默认值
	if cfg.Extension.TimeoutSeconds == 0 {
		cfg.Extension.TimeoutSeconds = 30
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
		// 默认不限存储：清理不可逆，漏写这一项不应该默默开始删历史条目。
		{"rss", "retention_unlimited", &cfg.RSS.RetentionUnlimited, true},
		// 下面两项默认关闭：开启会额外消耗资源，交给用户主动选择。
		{"balance_monitor", "enabled", &cfg.BalanceMonitor.Enabled, false},
		{"log", "db_writer", &cfg.Log.DBWriter, false},
		// 对接另一个实例，默认关闭。漏写这一项不应默默打开一条出站签名通道。
		{"extension", "enabled", &cfg.Extension.Enabled, false},
	}
}

// intFlag 描述一个「0 有含义、缺失另有默认值」的整数配置。
//
// 与 boolFlag 同理：int 零值无法区分「没写」与「写了 0」。
type intFlag struct {
	section  string
	field    string
	target   *int
	fallback int
}

// zeroableInts 汇总所有需要区分「缺失」与「显式 0」的整数项。
func (cfg *Config) zeroableInts() []intFlag {
	return []intFlag{
		// 0 = 不清理；漏写时按 30 天清理，避免日志表无限增长。
		{"log", "retention_days", &cfg.Log.RetentionDays, 30},
	}
}

// applyZeroableIntDefaults 为缺失的整数项填入默认值，显式值（含 0）保留。
func applyZeroableIntDefaults(raw map[string]json.RawMessage, cfg *Config) {
	for _, f := range cfg.zeroableInts() {
		if raw != nil && sectionHasKey(raw, f.section, f.field) {
			continue
		}
		*f.target = f.fallback
	}
}

// sectionHasKey 判断原始 JSON 的 section 段里是否写了 field 键。
func sectionHasKey(raw map[string]json.RawMessage, section, field string) bool {
	body, ok := raw[section]
	if !ok {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false
	}
	_, ok = fields[field]
	return ok
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
	applyZeroableIntDefaults(raw, cfg)
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
	// 缺字段或写成 null 时 Go 得到 nil 切片，会序列化成 null；前端按数组用它，
	// 遇到 null 直接抛错导致首页白屏。这里统一归一成空数组。
	if background.SelectedAssetIDs == nil {
		background.SelectedAssetIDs = []uint64{}
	}
}

// validProxyAddr 判断 s 是否为合法的 IP 或 CIDR。
func validProxyAddr(s string) bool {
	if strings.Contains(s, "/") {
		_, err := netip.ParsePrefix(s)
		return err == nil
	}
	_, err := netip.ParseAddr(s)
	return err == nil
}

// validate 校验配置的必填字段和取值范围。
//
// 返回聚合错误，一次性报告所有不合法项。
func validate(cfg *Config) error {
	var errs []string

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, "server.port 必须在 1-65535 之间")
	}
	for _, p := range cfg.Server.TrustedProxies {
		if !validProxyAddr(p) {
			errs = append(errs, fmt.Sprintf("server.trusted_proxies 含非法地址 %q，应为 IP 或 CIDR", p))
		}
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
	if cfg.Log.RetentionDays < 0 || cfg.Log.RetentionDays > 3650 {
		errs = append(errs, "log.retention_days 必须在 0-3650 之间（0 表示不限）")
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

	validProxyTypes := []string{"http", "socks5"}
	for i, p := range cfg.Proxy.Entries {
		if !slices.Contains(validProxyTypes, strings.ToLower(p.Type)) {
			errs = append(errs, fmt.Sprintf("proxy.entries[%d].type 必须为 http/socks5 之一", i))
		}
		if p.Name == "" {
			errs = append(errs, fmt.Sprintf("proxy.entries[%d].name 不能为空", i))
		}
	}
	// 默认代理链引用的名字必须真实存在，否则出网模块会静默直连。
	known := make(map[string]struct{}, len(cfg.Proxy.Entries))
	for _, p := range cfg.Proxy.Entries {
		known[p.Name] = struct{}{}
	}
	for i, name := range cfg.Proxy.Default {
		if _, ok := known[name]; !ok {
			errs = append(errs, fmt.Sprintf("proxy.default[%d] 引用了不存在的代理 %q", i, name))
		}
	}
	for i, name := range cfg.Extension.ProxyNames {
		if _, ok := known[name]; !ok {
			errs = append(errs, fmt.Sprintf("extension.proxy_names[%d] 引用了不存在的代理 %q", i, name))
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
	// 只有关掉无限存储时，保留数量才有意义；此时 0 或负数会被
	// enforceRetention 当成「未配置」回退到 5000，与用户预期不符。
	if !cfg.RSS.RetentionUnlimited && cfg.RSS.RetentionCount < 1 {
		errs = append(errs, "关闭 rss.retention_unlimited 时 retention_count 必须大于 0")
	}
	if cfg.RSS.FetchTimeoutSeconds < 5 || cfg.RSS.FetchTimeoutSeconds > 600 {
		errs = append(errs, "rss.fetch_timeout_seconds 必须在 5-600 之间")
	}
	if cfg.RSS.ImageCacheConcurrency < 1 || cfg.RSS.ImageCacheConcurrency > 32 {
		errs = append(errs, "rss.image_cache_concurrency 必须在 1-32 之间")
	}
	if cfg.RSS.ImageMaxBytes < 64*1024 || cfg.RSS.ImageMaxBytes > 2*1024*1024*1024 {
		errs = append(errs, "rss.image_max_bytes 必须在 65536-2147483648 (64KB-2GB) 之间")
	}
	if cfg.RSS.ImageRetentionDays < 1 || cfg.RSS.ImageRetentionDays > 3650 {
		errs = append(errs, "rss.image_retention_days 必须在 1-3650 之间")
	}
	if cfg.RSS.ImageCleanupIntervalHours < 1 || cfg.RSS.ImageCleanupIntervalHours > 168 {
		errs = append(errs, "rss.image_cleanup_interval_hours 必须在 1-168 之间")
	}

	// Extension 连接：只在启用时校验，否则留空是合法的
	anyFeature := cfg.Extension.Features.Hub || cfg.Extension.Features.Fetch || cfg.Extension.Features.Avatar
	if anyFeature && !cfg.Extension.Enabled {
		// 能力开关开着、总开关关着，几乎总是「以为开了功能实际没开」。
		// 不拦的话表现为：RSS 照常本地抓、头像照常直连、hub 报未启用，
		// 三处看起来都正常，唯独流量根本没走 Extension。
		errs = append(errs, "extension.features 有开关打开时 extension.enabled 必须为 true")
	}
	if cfg.Extension.Enabled {
		u, err := url.Parse(cfg.Extension.BaseURL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			errs = append(errs, "extension.base_url 必须是完整的 http(s) 地址，例如 https://ext.example.com/api/v1")
		case !strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), "/api/v1"):
			// 少写前缀会让所有请求打到 /hub/* 之外的路径，表现为一律 404，
			// 与“RSS 模块未启用”撞在同一个状态码上，很难查。这里提前拦下。
			errs = append(errs, "extension.base_url 必须以 /api/v1 结尾，例如 https://ext.example.com/api/v1")
		}
		// 私钥必须能解析成 Ed25519。格式错误在签名时才暴露，那时错误
		// 表现为每个请求都 401，很难定位到配置。
		if strings.TrimSpace(cfg.Extension.PrivateKeyPEM) == "" {
			errs = append(errs, "extension.enabled 为 true 时 extension.private_key_pem 不能为空")
		}
		if cfg.Extension.TimeoutSeconds < 1 || cfg.Extension.TimeoutSeconds > 300 {
			errs = append(errs, "extension.timeout_seconds 必须在 1-300 之间")
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
