// Package config 提供 HomeHub 应用配置的加载、热更新与并发安全读取。
//
// 配置以 JSON 单文件形式存储（默认 config.json），启动时加载，
// 运行时通过 fsnotify 监听文件变更自动热重载。
// 读取操作通过 atomic.Pointer 实现无锁访问，适合高频调用场景。
package config

import (
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

// LogConfig 日志配置。
type LogConfig struct {
	Level    string `json:"level"`     // 日志级别：debug / info / warn / error，默认 "info"
	Format   string `json:"format"`    // 日志格式：json / colored，默认 "json"
	DBWriter bool   `json:"db_writer"` // 是否将日志写入数据库，默认 true
}

// Config 应用顶层配置，对应 config.json 文件结构。
type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Auth     AuthConfig     `json:"auth"`
	Log      LogConfig      `json:"log"`
}

var (
	globalConfig atomic.Pointer[Config] // 当前生效的配置（无锁读取）
	mu           sync.RWMutex           // 保护热加载期间的临界区（预留）
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
