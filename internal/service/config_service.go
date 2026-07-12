package service

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
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
	Server       config.ServerConfig     `json:"server"`
	Database     SafeDatabaseConfig      `json:"database"`
	Auth         SafeAuthConfig          `json:"auth"`
	Log          config.LogConfig        `json:"log"`
	Background   config.BackgroundConfig `json:"background"`
	ProxyEnabled bool                    `json:"proxy_enabled"`
	Proxy        []SafeProxyEntry        `json:"proxy"`
	Gravatar     config.GravatarConfig   `json:"gravatar"`
	Bookmark     config.BookmarkConfig   `json:"bookmark"`
	RSS          config.RSSConfig        `json:"rss"`
	Bilibili     SafeBilibiliConfig      `json:"bilibili"`
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

// maskGravatarConfig 脱敏 Gravatar 配置中的自定义代理 URL。
func maskGravatarConfig(cfg config.GravatarConfig) config.GravatarConfig {
	cfg.CustomProxyURL = ""
	return cfg
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
		Log:          cfg.Log,
		Background:   cfg.Background,
		ProxyEnabled: cfg.ProxyEnabled,
		Proxy:        maskProxyList(cfg.Proxy),
		Gravatar:     maskGravatarConfig(cfg.Gravatar),
		Bookmark:     cfg.Bookmark,
		RSS:          cfg.RSS,
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
	Server       *ServerUpdate            `json:"server,omitempty"`
	Database     *DatabaseUpdate          `json:"database,omitempty"`
	Auth         *AuthUpdate              `json:"auth,omitempty"`
	Log          *LogUpdate               `json:"log,omitempty"`
	Background   *config.BackgroundConfig `json:"background,omitempty"`
	ProxyEnabled *bool                    `json:"proxy_enabled,omitempty"`
	Proxy        *[]config.ProxyEntry     `json:"proxy,omitempty"`
	Gravatar     *config.GravatarConfig   `json:"gravatar,omitempty"`
	Bookmark     *config.BookmarkConfig   `json:"bookmark,omitempty"`
	RSS          *config.RSSConfig        `json:"rss,omitempty"`
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
// 实现为"读取-合并-写回"模式。
// 写回成功后调用 config.Load 重新加载，触发热重载。
func (s *ConfigService) Update(req UpdateConfigRequest) error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("配置未加载")
	}

	// 合并更新（浅拷贝后按字段覆盖）
	merged := *cfg

	if req.Server != nil {
		if req.Server.Host != nil {
			merged.Server.Host = *req.Server.Host
		}
		if req.Server.Port != nil {
			merged.Server.Port = *req.Server.Port
		}
	}
	if req.Database != nil {
		if req.Database.Host != nil {
			merged.Database.Host = *req.Database.Host
		}
		if req.Database.Port != nil {
			merged.Database.Port = *req.Database.Port
		}
		if req.Database.User != nil {
			merged.Database.User = *req.Database.User
		}
		if req.Database.Password != nil {
			merged.Database.Password = *req.Database.Password
		}
		if req.Database.DBName != nil {
			merged.Database.DBName = *req.Database.DBName
		}
		if req.Database.SSLMode != nil {
			merged.Database.SSLMode = *req.Database.SSLMode
		}
	}
	if req.Auth != nil {
		if req.Auth.Username != nil {
			merged.Auth.Username = *req.Auth.Username
		}
		if req.Auth.Password != nil {
			merged.Auth.Password = *req.Auth.Password
		}
		if req.Auth.SessionSecret != nil {
			merged.Auth.SessionSecret = *req.Auth.SessionSecret
		}
	}
	if req.Log != nil {
		if req.Log.Level != nil {
			merged.Log.Level = *req.Log.Level
		}
		if req.Log.Format != nil {
			merged.Log.Format = *req.Log.Format
		}
		if req.Log.DBWriter != nil {
			merged.Log.DBWriter = *req.Log.DBWriter
		}
	}
	if req.Background != nil {
		merged.Background = *req.Background
	}
	if req.ProxyEnabled != nil {
		merged.ProxyEnabled = *req.ProxyEnabled
	}
	if req.Proxy != nil {
		merged.Proxy = *req.Proxy
	}
	if req.Gravatar != nil {
		merged.Gravatar = *req.Gravatar
	}
	if req.Bookmark != nil {
		merged.Bookmark = *req.Bookmark
	}
	if req.RSS != nil {
		merged.RSS = *req.RSS
	}

	// 写回 config.json
	data, err := json.MarshalIndent(merged, "", "    ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile("config.json", data, 0600); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	// 触发热重载
	if err := config.Load("config.json"); err != nil {
		return fmt.Errorf("重新加载配置失败: %w", err)
	}

	logger.Info("配置文件已通过 Web 端更新并热重载")
	return nil
}
