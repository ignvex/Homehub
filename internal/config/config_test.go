package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tempJSON 创建临时 JSON 文件并返回路径和清理函数。
func tempJSON(t *testing.T, content string) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入临时文件失败: %v", err)
	}
	return path, func() {} // t.TempDir 会在测试结束时自动清理
}

func TestLoad_ValidConfig(t *testing.T) {
	path, _ := tempJSON(t, `{
		"server": {"host": "127.0.0.1", "port": 9090},
		"database": {"host": "10.0.0.1", "port": 5432, "user": "app", "password": "secret", "dbname": "appdb", "sslmode": "require"},
		"auth": {"username": "admin", "password": "init", "session_secret": "abcdefghijklmnopqrstuvwxyz012345"},
		"log": {"level": "debug", "format": "colored", "db_writer": false}
	}`)

	if err := Load(path); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	cfg := Get()
	if cfg == nil {
		t.Fatal("Get() 返回 nil")
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "127.0.0.1")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9090)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "debug")
	}
	if cfg.Log.Format != "colored" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "colored")
	}
	if cfg.Log.DBWriter {
		t.Error("Log.DBWriter 应为 false")
	}
}

func TestLoad_Defaults(t *testing.T) {
	// 只提供必填字段，其余应使用默认值
	path, _ := tempJSON(t, `{
		"database": {"host": "10.0.0.1"},
		"auth": {"session_secret": "abcdefghijklmnopqrstuvwxyz012345"}
	}`)

	if err := Load(path); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	cfg := Get()
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want default %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want default %d", cfg.Server.Port, 8080)
	}
	if cfg.Database.Port != 5432 {
		t.Errorf("Database.Port = %d, want default %d", cfg.Database.Port, 5432)
	}
	if cfg.Database.SSLMode != "disable" {
		t.Errorf("Database.SSLMode = %q, want default %q", cfg.Database.SSLMode, "disable")
	}
	if cfg.Auth.Username != "admin" {
		t.Errorf("Auth.Username = %q, want default %q", cfg.Auth.Username, "admin")
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want default %q", cfg.Log.Level, "info")
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want default %q", cfg.Log.Format, "json")
	}
}

func TestLoad_DefaultConfigGenerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	err := Load(path)
	if err == nil {
		t.Fatal("预期 ErrConfigGenerated，但 err 为 nil")
	}
	if !errors.Is(err, ErrConfigGenerated) {
		t.Errorf("错误类型不匹配: %v", err)
	}

	// 验证文件已生成
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("默认配置文件未生成: %v", statErr)
	}
}

func TestLoad_ParseError(t *testing.T) {
	path, _ := tempJSON(t, `{invalid json!!!`)

	err := Load(path)
	if err == nil {
		t.Fatal("预期解析错误，但 err 为 nil")
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	cfg := &Config{}
	// 不设置 database.host 和 auth.session_secret
	err := validate(cfg)
	if err == nil {
		t.Fatal("预期校验错误，但 err 为 nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "database.host") {
		t.Error("错误信息应包含 database.host")
	}
	if !strings.Contains(errStr, "auth.session_secret") {
		t.Error("错误信息应包含 auth.session_secret")
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	cfg := &Config{Server: ServerConfig{Port: 99999}}
	err := validate(cfg)
	if err == nil {
		t.Fatal("预期端口校验错误")
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{Host: "localhost", User: "u", DBName: "d"},
		Auth:     AuthConfig{SessionSecret: "abcdefghijklmnopqrstuvwxyz012345"},
		Log:      LogConfig{Level: "verbose"},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("预期日志级别校验错误")
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{Host: "localhost", User: "u", DBName: "d"},
		Auth:     AuthConfig{SessionSecret: "abcdefghijklmnopqrstuvwxyz012345"},
		Log:      LogConfig{Level: "info", Format: "xml"},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("预期日志格式校验错误")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	// 验证 atomic.Pointer 的并发安全性
	path, _ := tempJSON(t, `{
		"database": {"host": "10.0.0.1"},
		"auth": {"session_secret": "abcdefghijklmnopqrstuvwxyz012345"}
	}`)

	if err := Load(path); err != nil {
		t.Fatalf("初始加载失败: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		// 读 goroutine
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cfg := Get()
				if cfg == nil {
					t.Error("并发读取返回 nil")
				}
			}
		}()
		// 写 goroutine（模拟热加载）
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = Load(path)
			}
		}()
	}
	wg.Wait()
}

func TestApplyDefaults_PartialJSON(t *testing.T) {
	raw := `{
		"server": {"port": 3000},
		"database": {"host": "db.local", "port": 0},
		"auth": {},
		"log": {}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}

	applyDefaults(&cfg)

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host 默认值未生效: %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 3000 {
		t.Errorf("Server.Port 应保留用户值 3000: %d", cfg.Server.Port)
	}
	// database.port=0 应被默认值覆盖
	if cfg.Database.Port != 5432 {
		t.Errorf("Database.Port 默认值未生效: %d", cfg.Database.Port)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level 默认值未生效: %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format 默认值未生效: %q", cfg.Log.Format)
	}
}

func TestLoad_NonExistentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "config.json")
	err := Load(path)
	if err == nil {
		t.Fatal("预期错误")
	}
	// 目录不存在时 createDefaultConfig 应该失败
	if errors.Is(err, ErrConfigGenerated) {
		t.Error("目录不存在时不应返回 ErrConfigGenerated")
	}
}

func TestWatch_StartsWithoutError(t *testing.T) {
	path, _ := tempJSON(t, `{
		"database": {"host": "10.0.0.1"},
		"auth": {"session_secret": "abcdefghijklmnopqrstuvwxyz012345"}
	}`)

	if err := Load(path); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// Watch 不应 panic
	Watch(path)

	// 给 watcher 一点时间初始化
	time.Sleep(100 * time.Millisecond)

	// 写入新配置触发热加载
	newContent := `{
		"server": {"host": "0.0.0.0", "port": 9999},
		"database": {"host": "10.0.0.2"},
		"auth": {"session_secret": "zyxwvutsrqponmlkjihgfedcba098765"}
	}`
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}

	// 等待热加载完成（防抖 150ms + 处理时间）
	time.Sleep(400 * time.Millisecond)

	cfg := Get()
	if cfg.Server.Port != 9999 {
		t.Errorf("热加载后 Server.Port = %d, want %d", cfg.Server.Port, 9999)
	}
}

func TestWatch_InvalidJSONKeepsOldConfig(t *testing.T) {
	path, _ := tempJSON(t, `{
		"database": {"host": "10.0.0.1"},
		"auth": {"session_secret": "abcdefghijklmnopqrstuvwxyz012345"}
	}`)

	if err := Load(path); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	Watch(path)
	time.Sleep(100 * time.Millisecond)

	// 写入非法 JSON
	if err := os.WriteFile(path, []byte(`{{{`), 0644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}

	time.Sleep(400 * time.Millisecond)

	// 配置应保持不变
	cfg := Get()
	if cfg.Database.Host != "10.0.0.1" {
		t.Errorf("热加载失败后配置被错误修改: Database.Host = %q", cfg.Database.Host)
	}
}
