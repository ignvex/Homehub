package service

import (
	"encoding/json"

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
	Proxy    []SafeProxyEntry      `json:"proxy"`
	Gravatar SafeGravatarConfig    `json:"gravatar"`
	Bookmark config.BookmarkConfig `json:"bookmark"`
	RSS      SafeRSSConfig         `json:"rss"`
	// ExtensionHub 含私钥**路径**而不是内容。路径本身不是秘密，
	// 而设置页需要它才能显示当前指向哪个文件。
	ExtensionHub config.ExtensionHubConfig `json:"extension_hub"`
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

// SafeGravatarConfig 是脱敏后的头像配置。
//
// 嵌入了原结构体，所以 JSON 形状不变；额外多一个 extension_token_set，
// 让设置页能显示「已配置」而不需要拿到 token 本身。
type SafeGravatarConfig struct {
	config.GravatarConfig
	ExtensionTokenSet bool `json:"extension_token_set"`
}

// SafeRSSConfig 是脱敏后的 RSS 配置，理由同 SafeGravatarConfig。
type SafeRSSConfig struct {
	config.RSSConfig
	ExtensionTokenSet bool `json:"extension_token_set"`
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
		Proxy:    maskProxyList(cfg.Proxy),
		Gravatar: maskGravatar(cfg.Gravatar),
		Bookmark: cfg.Bookmark,
		RSS:      maskRSS(cfg.RSS),
		ExtensionHub: config.ExtensionHubConfig{
			Enabled:        cfg.ExtensionHub.Enabled,
			BaseURL:        cfg.ExtensionHub.BaseURL,
			PrivateKeyPath: cfg.ExtensionHub.PrivateKeyPath,
			TimeoutSeconds: cfg.ExtensionHub.TimeoutSeconds,
		},
	}
}

// maskGravatar 清空 extension_token，只保留「有没有配」这一位信息。
//
// 之前这里直接返回完整的 GravatarConfig —— token 会随 GET /config
// 明文下发到浏览器，然后停留在开发者工具、前端内存和任何抓取里。
// 现在只返回空串，前端据「留空表示不改」的约定保留原值。
func maskGravatar(in config.GravatarConfig) SafeGravatarConfig {
	hasToken := in.ExtensionToken != ""
	out := in
	out.ExtensionToken = ""
	return SafeGravatarConfig{GravatarConfig: out, ExtensionTokenSet: hasToken}
}

// maskRSS 清空 extension_token，理由同 maskGravatar。
func maskRSS(in config.RSSConfig) SafeRSSConfig {
	hasToken := in.ExtensionToken != ""
	out := in
	out.ExtensionToken = ""
	return SafeRSSConfig{RSSConfig: out, ExtensionTokenSet: hasToken}
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
	Proxy    *[]config.ProxyEntry   `json:"proxy,omitempty"`
	Gravatar *config.GravatarConfig `json:"gravatar,omitempty"`
	Bookmark *config.BookmarkConfig `json:"bookmark,omitempty"`
	RSS      *config.RSSConfig      `json:"rss,omitempty"`
	// ExtensionHub 只接受路径与开关；私钥内容永远不会经过这里。
	ExtensionHub *config.ExtensionHubConfig `json:"extension_hub,omitempty"`
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
			raw["proxy"] = mergeProxies(raw["proxy"], *req.Proxy)
		}
		if req.Gravatar != nil {
			replaceSection(raw, "gravatar", req.Gravatar, "extension_token")
		}
		if req.Bookmark != nil {
			replaceSection(raw, "bookmark", req.Bookmark)
		}
		if req.RSS != nil {
			replaceSection(raw, "rss", req.RSS, "extension_token")
		}
		if req.ExtensionHub != nil {
			replaceSection(raw, "extension_hub", req.ExtensionHub)
		}
		return nil
	})
}

// mergeProxies 按名字合并代理条目。
//
// SafeConfig 出于安全不返回代理地址（http_url/https_url），所以设置页
// 回传的条目里这两项必然为空。整体替换会让每次保存设置都清掉代理凭据，
// 代理随即失效——而界面上看起来只是「又保存了一次」。
// 名字匹配不上的算新增条目，用请求里的值（前端目前只能通过 JSON 编辑
// 新增代理，这样至少不会把已有条目弄坏）。
func mergeProxies(existing any, incoming []config.ProxyEntry) any {
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
