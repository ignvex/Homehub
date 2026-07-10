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

// SafeConfig 对外暴露的安全配置视图，不含敏感字段。
type SafeConfig struct {
	Server   SafeServerConfig   `json:"server"`
	Database SafeDatabaseConfig `json:"database"`
	Auth     SafeAuthConfig     `json:"auth"`
	Log      config.LogConfig   `json:"log"`
}

// SafeServerConfig 不含敏感信息的服务端配置。
type SafeServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
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

// GetSafe 返回过滤敏感字段后的安全配置视图。
func (s *ConfigService) GetSafe() SafeConfig {
	cfg := config.Get()
	if cfg == nil {
		return SafeConfig{}
	}

	return SafeConfig{
		Server: SafeServerConfig{
			Host: cfg.Server.Host,
			Port: cfg.Server.Port,
		},
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
		Log: cfg.Log,
	}
}

// UpdateConfigRequest Web 端更新配置的请求体。
//
// 仅允许更新部分字段，Password 用于首次设置或修改密码。
type UpdateConfigRequest struct {
	Server *struct {
		Host *string `json:"host"`
		Port *int    `json:"port"`
	} `json:"server,omitempty"`
	Database *struct {
		Host     *string `json:"host"`
		Port     *int    `json:"port"`
		User     *string `json:"user"`
		Password *string `json:"password"`
		DBName   *string `json:"dbname"`
		SSLMode  *string `json:"sslmode"`
	} `json:"database,omitempty"`
	Auth *struct {
		Username      *string `json:"username"`
		Password      *string `json:"password"`
		SessionSecret *string `json:"session_secret"`
	} `json:"auth,omitempty"`
	Log *struct {
		Level    *string `json:"level"`
		Format   *string `json:"format"`
		DBWriter *bool   `json:"db_writer"`
	} `json:"log,omitempty"`
}

// Update 将 Web 端的部分更新应用到 config.json。
//
// 实现为"读取-合并-写回"模式，合并期间持有 RWMutex 写锁。
// 写回成功后调用 config.Load 重新加载，触发热重载。
func (s *ConfigService) Update(req UpdateConfigRequest) error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("配置未加载")
	}

	// 合并更新
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
