package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathFollowsLoadedFile 确认 Path() 返回实际加载的配置文件路径。
//
// HOMEHUB_CONFIG 指向自定义路径时，Web 端配置更新与密码清除必须写回同一文件。
func TestPathFollowsLoadedFile(t *testing.T) {
	bak := Path()
	t.Cleanup(func() { loadedPath.Store(bak) })

	dir := t.TempDir()
	p := filepath.Join(dir, "custom-config.json")
	body := `{"database":{"host":"127.0.0.1","port":5432,"user":"u","dbname":"d"},"auth":{"session_secret":"` + strings.Repeat("a", 32) + `"},"log":{"level":"info","format":"json"}}`
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Path(); got != p {
		t.Fatalf("Path() = %q, want %q", got, p)
	}
}

// TestPathDefault 确认尚未加载配置时 Path() 回落到 "config.json"。
func TestPathDefault(t *testing.T) {
	bak, hadValue := loadedPath.Load().(string)
	t.Cleanup(func() {
		if hadValue {
			loadedPath.Store(bak)
		}
	})
	loadedPath.Store("")

	if got := Path(); got != "config.json" {
		t.Fatalf("Path() = %q, want %q", got, "config.json")
	}
}

// TestExampleConfigIsValid 确认仓库中的 config.example.json 通过配置校验。
//
// 防止示例配置随字段增减而腐化，用户复制后即可启动到“填写凭据”这一步。
func TestExampleConfigIsValid(t *testing.T) {
	bak := Path()
	t.Cleanup(func() { loadedPath.Store(bak) })

	if err := Load(filepath.Join("..", "..", "config.example.json")); err != nil {
		t.Fatalf("config.example.json 未通过校验: %v", err)
	}
}

// TestBoolFlagsDefaults 确认所有"缺省即启用"的开关在键缺失时取默认值。
//
// bool 零值是 false：不显式处理的话，用户漏写一项就会让功能静默关闭。
func TestBoolFlagsDefaults(t *testing.T) {
	bak := Path()
	t.Cleanup(func() { loadedPath.Store(bak) })

	tmp := filepath.Join(t.TempDir(), "missing-flags.json")
	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "info", "format": "json"}
	}`
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Load(tmp); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := Get()
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"gravatar.enabled", cfg.Gravatar.Enabled, true},
		{"pastebin.enabled", cfg.Pastebin.Enabled, true},
		{"rss.background_fetch_enabled", cfg.RSS.BackgroundFetchEnabled, true},
		{"rss.image_cache_enabled", cfg.RSS.ImageCacheEnabled, true},
		{"balance_monitor.enabled", cfg.BalanceMonitor.Enabled, false},
		{"log.db_writer", cfg.Log.DBWriter, false},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v，期望 %v", c.name, c.got, c.want)
		}
	}
}

// TestBoolFlagsExplicitRespected 确认显式写出的值不会被默认值覆盖。
func TestBoolFlagsExplicitRespected(t *testing.T) {
	bak := Path()
	t.Cleanup(func() { loadedPath.Store(bak) })

	tmp := filepath.Join(t.TempDir(), "explicit-flags.json")
	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "info", "format": "json", "db_writer": true},
		"gravatar": {"enabled": false},
		"pastebin": {"enabled": false},
		"rss": {"background_fetch_enabled": false, "image_cache_enabled": false},
		"balance_monitor": {"enabled": true}
	}`
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Load(tmp); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := Get()
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"gravatar.enabled", cfg.Gravatar.Enabled, false},
		{"pastebin.enabled", cfg.Pastebin.Enabled, false},
		{"rss.background_fetch_enabled", cfg.RSS.BackgroundFetchEnabled, false},
		{"rss.image_cache_enabled", cfg.RSS.ImageCacheEnabled, false},
		{"balance_monitor.enabled", cfg.BalanceMonitor.Enabled, true},
		{"log.db_writer", cfg.Log.DBWriter, true},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v，期望 %v（显式值被默认值覆盖）", c.name, c.got, c.want)
		}
	}
}

// TestGeneratedDefaultConfigHasFlagsOn 确认自动生成的默认配置包含启用的开关。
//
// 这条防的是"默认值只加在已有文件的加载路径、忘了新建路径"这类疏漏：
// 用户按提示 cp 默认配置后启动，剪贴板与头像本应立即可用。
func TestGeneratedDefaultConfigHasFlagsOn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")

	// 文件不存在 → 生成默认配置并返回 ErrConfigGenerated
	if err := Load(p); !errors.Is(err, ErrConfigGenerated) {
		t.Fatalf("期望 ErrConfigGenerated，实际 %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	get := func(section, field string) bool {
		s, _ := m[section].(map[string]any)
		v, _ := s[field].(bool)
		return v
	}

	if !get("gravatar", "enabled") {
		t.Error("生成的默认配置 gravatar.enabled 应为 true")
	}
	if !get("pastebin", "enabled") {
		t.Error("生成的默认配置 pastebin.enabled 应为 true")
	}
	if !get("rss", "background_fetch_enabled") {
		t.Error("生成的默认配置 rss.background_fetch_enabled 应为 true")
	}
	if !get("rss", "image_cache_enabled") {
		t.Error("生成的默认配置 rss.image_cache_enabled 应为 true")
	}
}
