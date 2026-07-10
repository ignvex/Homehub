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

	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}
