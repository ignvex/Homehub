package service

import (
	"encoding/json"
	"fmt"

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
	Gravatar config.GravatarConfig `json:"gravatar"`
	Bookmark config.BookmarkConfig `json:"bookmark"`
	RSS      config.RSSConfig      `json:"rss"`
	Bilibili SafeBilibiliConfig    `json:"bilibili"`
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

// SafeBilibiliConfig 不含 SESSDATA 等敏感字段的 Bilibili 配置视图。
type SafeBilibiliConfig struct {
	Enabled        bool   `json:"enabled"`
	VideoEnabled   bool   `json:"video_enabled"`
	ArticleEnabled bool   `json:"article_enabled"`
	UserEnabled    bool   `json:"user_enabled"`
	UserAgent      string `json:"user_agent"`
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
		Gravatar: cfg.Gravatar,
		Bookmark: cfg.Bookmark,
		RSS:      cfg.RSS,
		Bilibili: SafeBilibiliConfig{
			Enabled:        cfg.Bilibili.Enabled,
			VideoEnabled:   cfg.Bilibili.VideoEnabled,
			ArticleEnabled: cfg.Bilibili.ArticleEnabled,
			UserEnabled:    cfg.Bilibili.UserEnabled,
			UserAgent:      cfg.Bilibili.UserAgent,
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
	Proxy    *[]config.ProxyEntry   `json:"proxy,omitempty"`
	Gravatar *config.GravatarConfig `json:"gravatar,omitempty"`
	Bookmark *config.BookmarkConfig `json:"bookmark,omitempty"`
	RSS      *config.RSSConfig      `json:"rss,omitempty"`
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
	return config.AtomicJSONUpdate("config.json", func(raw map[string]any) error {
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
			raw["proxy"] = structToMap(*req.Proxy)
		}
		if req.Gravatar != nil {
			raw["gravatar"] = structToMap(*req.Gravatar)
		}
		if req.Bookmark != nil {
			raw["bookmark"] = structToMap(*req.Bookmark)
		}
		if req.RSS != nil {
			raw["rss"] = structToMap(*req.RSS)
		}
		return nil
	})
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

// ToggleBilibiliCrawler 切换指定 Bilibili 爬虫模块的启用状态。
//
// 通过修改 config.json 中对应的 _enabled 字段并触发热重载实现。
// 启用子模块时总开关自动开启。使用 AtomicJSONUpdate 保留 _comment 字段。
func (s *ConfigService) ToggleBilibiliCrawler(crawlerType string, enabled bool) error {
	return config.AtomicJSONUpdate("config.json", func(raw map[string]any) error {
		ensureMap(raw, "bilibili")
		bili := raw["bilibili"].(map[string]any)

		if enabled {
			bili["enabled"] = true
		}

		switch crawlerType {
		case "video":
			bili["video_enabled"] = enabled
		case "article":
			bili["article_enabled"] = enabled
		case "user":
			bili["user_enabled"] = enabled
		default:
			return fmt.Errorf("无效的爬虫类型: %s", crawlerType)
		}
		return nil
	})
}
