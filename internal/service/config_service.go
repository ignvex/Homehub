package service

import (
	"encoding/json"
	"strings"

	"github.com/ignvex/HomeHub/internal/config"
)

// ConfigService 配置管理业务服务。
//
// 负责安全读取当前配置（过滤敏感字段）和 Web 端更新 config.json。
type ConfigService struct{}

// NewConfigService 创建 ConfigService 实例。
func NewConfigService() *ConfigService {
	return &ConfigService{}
}

// SafeConfig 对外暴露的安全配置视图，不含数据库密码、session_secret 等敏感字段。
type SafeConfig struct {
	Server   config.ServerConfig   `json:"server"`
	Database SafeDatabaseConfig    `json:"database"`
	Auth     SafeAuthConfig        `json:"auth"`
	Log      config.LogConfig      `json:"log"`
	WebUI    config.WebUIConfig    `json:"webui"`
	Proxy    SafeProxyConfig       `json:"proxy"`
	Gravatar config.GravatarConfig `json:"gravatar"`
	Bookmark config.BookmarkConfig `json:"bookmark"`
	RSS      config.RSSConfig      `json:"rss"`
	// Extension 含连接参数，但**绝不含私钥内容**。
	Extension SafeExtensionConfig `json:"extension"`
}

// SafeDatabaseConfig 不含密码的数据库配置。
type SafeDatabaseConfig struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	DBName  string `json:"dbname"`
	SSLMode string `json:"sslmode"`
}

// SafeAuthConfig 不含 session_secret 的认证配置。
type SafeAuthConfig struct {
	Username string `json:"username"`
}

// SafeExtensionConfig 是脱敏后的 Extension 连接配置。
//
// 私钥永不通过 API 返回：它是宿主的身份，泄露等于任何人都能以宿主
// 名义驱动 Extension。这里只回一位 PrivateKeySet，让设置页能显示
// 「已配置 / 未配置」。
type SafeExtensionConfig struct {
	Enabled        bool     `json:"enabled"`
	BaseURL        string   `json:"base_url"`
	ProxyNames     []string `json:"proxy_names"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	PrivateKeySet  bool     `json:"private_key_set"`
}

// SafeProxyConfig 是脱敏后的全局代理配置。
//
// 代理 URL 常含账号密码（http://user:pass@host:port），一律不下发；
// 名字与启停状态足以让设置页渲染列表。
type SafeProxyConfig struct {
	Entries []SafeProxyEntry `json:"entries"`
	Default []string         `json:"default"`
}

// ExtensionUpdate 是 Web 端可修改的 Extension 连接字段。
//
// 不含私钥：私钥通过单独的 setKey 端点设置，避免它出现在任何
// 「读配置 → 改一处 → 提交整个对象」的表单流程里。
type ExtensionUpdate struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	BaseURL        *string  `json:"base_url,omitempty"`
	ProxyNames     *[]string `json:"proxy_names,omitempty"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty"`
}

// SafeProxyEntry 脱敏后的代理配置，不暴露完整的代理 URL。
type SafeProxyEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

// maskProxyList 将代理列表脱敏。
func maskProxyList(proxies []config.ProxyEntry) []SafeProxyEntry {
	result := make([]SafeProxyEntry, len(proxies))
	for i, p := range proxies {
		result[i] = maskProxyEntry(p)
	}
	return result
}

// maskProxyEntry 将 ProxyEntry 脱敏为 SafeProxyEntry。
func maskProxyEntry(p config.ProxyEntry) SafeProxyEntry {
	return SafeProxyEntry{
		Name:    p.Name,
		Type:    p.Type,
		Enabled: p.Enabled,
	}
}

// GetSafe 返回过滤敏感字段后的安全配置视图。
func (s *ConfigService) GetSafe() SafeConfig {
	cfg := config.Get()
	if cfg == nil {
		return SafeConfig{}
	}

	return SafeConfig{
		Server: cfg.Server,
		Database: SafeDatabaseConfig{
			Host:    cfg.Database.Host,
			Port:    cfg.Database.Port,
			User:    cfg.Database.User,
			DBName:  cfg.Database.DBName,
			SSLMode: cfg.Database.SSLMode,
		},
		Auth: SafeAuthConfig{
			Username: cfg.Auth.Username,
		},
		Log:      cfg.Log,
		WebUI:    cfg.WebUI,
		Proxy: SafeProxyConfig{
			Entries: maskProxyList(cfg.Proxy.Entries),
			Default: cfg.Proxy.Default,
		},
		Gravatar: cfg.Gravatar,
		Bookmark: cfg.Bookmark,
		RSS:      cfg.RSS,
		Extension: SafeExtensionConfig{
			Enabled:        cfg.Extension.Enabled,
			BaseURL:        cfg.Extension.BaseURL,
			ProxyNames:     cfg.Extension.ProxyNames,
			TimeoutSeconds: cfg.Extension.TimeoutSeconds,
			PrivateKeySet:  strings.TrimSpace(cfg.Extension.PrivateKeyPEM) != "",
		},
	}
}

// UpdateConfigRequest Web 端更新配置的请求体。
//
// 所有顶级字段均为可选（指针类型），nil 表示不修改该部分。
type UpdateConfigRequest struct {
	Server   *ServerUpdate          `json:"server,omitempty"`
	Database *DatabaseUpdate        `json:"database,omitempty"`
	Auth     *AuthUpdate            `json:"auth,omitempty"`
	Log      *LogUpdate             `json:"log,omitempty"`
	WebUI    *config.WebUIConfig    `json:"webui,omitempty"`
	Proxy    *config.ProxyConfig    `json:"proxy,omitempty"`
	Gravatar *config.GravatarConfig `json:"gravatar,omitempty"`
	Bookmark *config.BookmarkConfig `json:"bookmark,omitempty"`
	RSS      *config.RSSConfig      `json:"rss,omitempty"`
	// Extension 只接受连接参数；私钥走单独的 setKey 端点，不进这个结构体。
	Extension *ExtensionUpdate `json:"extension,omitempty"`
}

// ServerUpdate 服务器配置更新字段。
type ServerUpdate struct {
	Host *string `json:"host,omitempty"`
	Port *int    `json:"port,omitempty"`
}

// DatabaseUpdate 数据库配置更新字段。
type DatabaseUpdate struct {
	Host     *string `json:"host,omitempty"`
	Port     *int    `json:"port,omitempty"`
	User     *string `json:"user,omitempty"`
	Password *string `json:"password,omitempty"`
	DBName   *string `json:"dbname,omitempty"`
	SSLMode  *string `json:"sslmode,omitempty"`
}

// AuthUpdate 认证配置更新字段。
type AuthUpdate struct {
	Username      *string `json:"username,omitempty"`
	Password      *string `json:"password,omitempty"`
	SessionSecret *string `json:"session_secret,omitempty"`
}

// LogUpdate 日志配置更新字段。
type LogUpdate struct {
	Level    *string `json:"level,omitempty"`
	Format   *string `json:"format,omitempty"`
	DBWriter *bool   `json:"db_writer,omitempty"`
}

// Update 将 Web 端的部分更新应用到 config.json。
//
// 使用 AtomicJSONUpdate 保留 JSON 中的非结构体字段（如 _comment）。
// 写回成功后触发热重载。
func (s *ConfigService) Update(req UpdateConfigRequest) error {
	return config.AtomicJSONUpdate(config.Path(), func(raw map[string]any) error {
		if req.Server != nil {
			ensureMap(raw, "server")
			srv := raw["server"].(map[string]any)
			if req.Server.Host != nil {
				srv["host"] = *req.Server.Host
			}
			if req.Server.Port != nil {
				srv["port"] = float64(*req.Server.Port)
			}
		}
		if req.Database != nil {
			ensureMap(raw, "database")
			db := raw["database"].(map[string]any)
			if req.Database.Host != nil {
				db["host"] = *req.Database.Host
			}
			if req.Database.Port != nil {
				db["port"] = float64(*req.Database.Port)
			}
			if req.Database.User != nil {
				db["user"] = *req.Database.User
			}
			if req.Database.Password != nil {
				db["password"] = *req.Database.Password
			}
			if req.Database.DBName != nil {
				db["dbname"] = *req.Database.DBName
			}
			if req.Database.SSLMode != nil {
				db["sslmode"] = *req.Database.SSLMode
			}
		}
		if req.Auth != nil {
			ensureMap(raw, "auth")
			auth := raw["auth"].(map[string]any)
			if req.Auth.Username != nil {
				auth["username"] = *req.Auth.Username
			}
			if req.Auth.Password != nil {
				auth["password"] = *req.Auth.Password
			}
			if req.Auth.SessionSecret != nil {
				auth["session_secret"] = *req.Auth.SessionSecret
			}
		}
		if req.Log != nil {
			ensureMap(raw, "log")
			logMap := raw["log"].(map[string]any)
			if req.Log.Level != nil {
				logMap["level"] = *req.Log.Level
			}
			if req.Log.Format != nil {
				logMap["format"] = *req.Log.Format
			}
			if req.Log.DBWriter != nil {
				logMap["db_writer"] = *req.Log.DBWriter
			}
		}
		if req.WebUI != nil {
			raw["webui"] = structToMap(*req.WebUI)
		}
		if req.Proxy != nil {
			raw["proxy"] = mergeProxyConfig(raw["proxy"], *req.Proxy)
		}
		if req.Gravatar != nil {
			replaceSection(raw, "gravatar", req.Gravatar)
		}
		if req.Bookmark != nil {
			replaceSection(raw, "bookmark", req.Bookmark)
		}
		if req.RSS != nil {
			replaceSection(raw, "rss", req.RSS)
		}
		if req.Extension != nil {
			mergeExtension(raw, *req.Extension)
		}
		return nil
	})
}

// mergeProxyConfig 按名字合并代理条目，并处理默认链。
//
// SafeConfig 出于安全不返回代理地址（http_url/https_url），所以设置页
// 回传的条目里这两项必然为空。整体替换会让每次保存设置都清掉代理凭据，
// 代理随即失效——而界面上看起来只是「又保存了一次」。
// 名字匹配不上的算新增条目，用请求里的值。
//
// Default 为 nil 时保留旧值：请求体里省略该字段表达的是「不改」，
// 与空数组「明确清空」是两件事。
func mergeProxyConfig(existing any, incoming config.ProxyConfig) any {
	var oldDefault any
	if m, ok := existing.(map[string]any); ok {
		if v, exists := m["default"]; exists {
			oldDefault = v
		}
	}

	merged := mergeProxies(nil, incoming.Entries)
	if existing != nil {
		if m, ok := existing.(map[string]any); ok {
			merged = mergeProxies(m["entries"], incoming.Entries)
		}
	}

	out := map[string]any{"entries": merged}
	switch {
	case incoming.Default != nil:
		out["default"] = incoming.Default
	case oldDefault != nil:
		out["default"] = oldDefault
	}
	return out
}

// mergeProxies 按名字合并代理条目。existing 为原始 JSON 里的 entries 数组。
func mergeProxies(existing any, incoming []config.ProxyEntry) []any {
	oldByName := make(map[string]map[string]any, len(incoming))
	if list, ok := existing.([]any); ok {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := m["name"].(string); ok {
				oldByName[name] = m
			}
		}
	}

	out := make([]any, 0, len(incoming))
	for _, entry := range incoming {
		m, _ := structToMap(entry).(map[string]any)
		if old, ok := oldByName[entry.Name]; ok {
			for _, field := range []string{"http_url", "https_url"} {
				if current, _ := m[field].(string); current == "" {
					if v, exists := old[field]; exists {
						m[field] = v
					}
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// mergeExtension 合并 Extension 连接字段，逐字段覆盖。
//
// 不能用 replaceSection 整体替换：那样会丢掉 private_key_pem —— 它不在
// ExtensionUpdate 里，但必须原样保留，否则「改一下超时」就会把宿主的
// 签名身份清空。
func mergeExtension(raw map[string]any, update ExtensionUpdate) {
	section, ok := raw["extension"].(map[string]any)
	if !ok {
		section = map[string]any{}
		raw["extension"] = section
	}
	if update.Enabled != nil {
		section["enabled"] = *update.Enabled
	}
	if update.BaseURL != nil {
		section["base_url"] = *update.BaseURL
	}
	if update.ProxyNames != nil {
		section["proxy_names"] = *update.ProxyNames
	}
	if update.TimeoutSeconds != nil {
		section["timeout_seconds"] = *update.TimeoutSeconds
	}
}

// replaceSection 用新值替换 raw[key]，但保留两类旧内容：
//
//  1. 不在结构体里的键（`_comment`、用户自己加的说明）。设置页每模块
//     单独保存时只回传它认识的字段，整体覆盖会把注释清掉。
//  2. keepEmpty 列出的键：新值为空串时保留旧值。脱敏后的 token 就是
//     空串，若直接写入等于每次保存设置都会把 token 抹掉。
func replaceSection(raw map[string]any, key string, value any, keepEmpty ...string) {
	incoming, ok := structToMap(value).(map[string]any)
	if !ok {
		raw[key] = structToMap(value)
		return
	}
	if existing, ok := raw[key].(map[string]any); ok {
		for k, v := range existing {
			if _, exists := incoming[k]; !exists {
				incoming[k] = v
			}
		}
		for _, field := range keepEmpty {
			if s, isStr := incoming[field].(string); isStr && s == "" {
				if old, isStr := existing[field].(string); isStr && old != "" {
					incoming[field] = old
				}
			}
		}
	}
	raw[key] = incoming
}

// ensureMap 确保 raw[key] 为 map[string]any。
func ensureMap(raw map[string]any, key string) {
	if _, ok := raw[key].(map[string]any); !ok {
		raw[key] = map[string]any{}
	}
}

// structToMap 将任意值通过 JSON 往返转换为 map/切片等通用类型。
func structToMap(v any) any {
	data, _ := json.Marshal(v)
	var result any
	json.Unmarshal(data, &result)
	return result
}
