package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// 哨兵错误：首次启动时 config.json 不存在，已生成默认配置文件。
var ErrConfigGenerated = errors.New("默认配置文件已生成，请编辑后重新启动")

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

	if err := validate(&cfg); err != nil {
		return err
	}

	atomicStore(&cfg)
	return nil
}

// createDefaultConfig 写入带默认值的配置文件并返回 ErrConfigGenerated。
func createDefaultConfig(path string) error {
	cfg := Config{}
	applyDefaults(&cfg)

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

	if cfg.Background.Mode == "" {
		cfg.Background.Mode = "single"
	}
	if cfg.Background.IntervalSeconds == 0 {
		cfg.Background.IntervalSeconds = 30
	}
	if cfg.Background.TransitionEffect == "" {
		cfg.Background.TransitionEffect = "fade"
	}
	if cfg.Background.FadeDurationMs == 0 {
		cfg.Background.FadeDurationMs = 1000
	}

	applyBackgroundDefaults(&cfg.WebUI.Home.Background, cfg.Background)
	applyBackgroundDefaults(&cfg.WebUI.Login.Background, cfg.Background)
	if cfg.WebUI.Login.Layout == "" {
		cfg.WebUI.Login.Layout = "center"
	}

	if cfg.Gravatar.DefaultStyle == "" {
		cfg.Gravatar.DefaultStyle = "identicon"
	}
	if cfg.Gravatar.DefaultSize == 0 {
		cfg.Gravatar.DefaultSize = 200
	}
	if cfg.Gravatar.Rating == "" {
		cfg.Gravatar.Rating = "g"
	}

	for i := range cfg.Proxy {
		if cfg.Proxy[i].Type == "" {
			cfg.Proxy[i].Type = "http"
		}
	}

	// Bilibili 子开关默认关闭（用户需显式开启，避免误启动大规模抓取）
}

func applyBackgroundDefaults(background *BackgroundConfig, fallback BackgroundConfig) {
	// 仅当字段为零值时从 fallback 复制（逐字段 fallback，避免整体替换覆盖用户显式设置）。
	if background.Mode == "" {
		background.Mode = fallback.Mode
	}
	if background.IntervalSeconds == 0 {
		background.IntervalSeconds = fallback.IntervalSeconds
	}
	if background.TransitionEffect == "" {
		background.TransitionEffect = fallback.TransitionEffect
	}
	if background.FadeDurationMs == 0 {
		background.FadeDurationMs = fallback.FadeDurationMs
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

	if len(cfg.Auth.SessionSecret) < 32 {
		errs = append(errs, "auth.session_secret 长度不能少于 32 字符")
	}

	if !slices.Contains(validLogLevels, strings.ToLower(cfg.Log.Level)) {
		errs = append(errs, "log.level 必须为 debug/info/warn/error 之一")
	}
	if !slices.Contains(validLogFormats, strings.ToLower(cfg.Log.Format)) {
		errs = append(errs, "log.format 必须为 json/colored 之一")
	}

	if !slices.Contains(validBackgroundModes, strings.ToLower(cfg.Background.Mode)) {
		errs = append(errs, "background.mode 必须为 single/carousel/random 之一")
	}
	if cfg.Background.IntervalSeconds < 1 || cfg.Background.IntervalSeconds > 3600 {
		errs = append(errs, "background.interval_seconds 必须在 1-3600 之间")
	}
	if !slices.Contains(validBackgroundTransitions, strings.ToLower(cfg.Background.TransitionEffect)) {
		errs = append(errs, "background.transition_effect 必须为 fade/slide 之一")
	}
	if cfg.Background.FadeDurationMs < 0 || cfg.Background.FadeDurationMs > 10000 {
		errs = append(errs, "background.fade_duration_ms 必须在 0-10000 之间")
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
	if cfg.Gravatar.DefaultSize < 1 || cfg.Gravatar.DefaultSize > 2048 {
		errs = append(errs, "gravatar.default_size 必须在 1-2048 之间")
	}
	validGravatarRatings := []string{"g", "pg", "r", "x"}
	if !slices.Contains(validGravatarRatings, strings.ToLower(cfg.Gravatar.Rating)) {
		errs = append(errs, "gravatar.rating 必须为 g/pg/r/x 之一")
	}

	validProxyTypes := []string{"http", "socks5"}
	for i, p := range cfg.Proxy {
		if !slices.Contains(validProxyTypes, strings.ToLower(p.Type)) {
			errs = append(errs, fmt.Sprintf("proxy[%d].type 必须为 http/socks5 之一", i))
		}
	}

	// Bilibili：如果填了 SESSDATA，必须配对 bili_jct
	if cfg.Bilibili.SESSDATA != "" {
		if cfg.Bilibili.BiliJCT == "" {
			errs = append(errs, "bilibili.bili_jct 不能为空（SESSDATA 已配置时必须填写）")
		}
		if cfg.Bilibili.DedeUserID == "" {
			errs = append(errs, "bilibili.dede_user_id 不能为空（SESSDATA 已配置时必须填写）")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}
