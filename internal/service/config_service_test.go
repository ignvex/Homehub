package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ignvex/HomeHub/internal/config"
)

// readConfigJSON 读回配置文件，用于断言实际写入的形状。
func readConfigJSON(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	return out
}

// loadSettingsConfig 写一份带 extension 配置的最小配置并加载。
func loadSettingsConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return path
}

const settingsBaseConfig = `{
	"_comment": "顶层说明，保存设置时不应丢失",
	"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d", "sslmode": "disable"},
	"auth": {"session_secret": "%s"},
	"log": {"level": "info", "format": "json", "db_writer": false},
	"gravatar": {
		"_comment": "头像说明",
		"enabled": true,
		"default_style": "identicon",
		"default_size": 256,
		"rating": "g",
		"extension_url": "http://ext:8090",
		"extension_token": "super-secret-gravatar-token",
		"timeout_seconds": 10,
		"max_bytes": 2097152
	},
	"rss": {
		"background_fetch_enabled": true,
		"retention_count": 5000,
		"extension_url": "http://ext:8090",
		"extension_token": "super-secret-rss-token"
	},
	"proxy": [
		{"name": "router", "type": "http", "http_url": "http://127.0.0.1:7890", "https_url": "http://127.0.0.1:7890", "enabled": true}
	],
	"extension_hub": {"enabled": false, "base_url": "", "private_key_path": "", "timeout_seconds": 30}
}`

// TestSafeConfigNeverReturnsExtensionTokens 是最重要的一条。
//
// 之前 GetSafe 直接返回完整的 GravatarConfig / RSSConfig，token 会随
// GET /config 明文下发到浏览器——开发者工具、前端内存、任何抓包都能拿到。
func TestSafeConfigNeverReturnsExtensionTokens(t *testing.T) {
	loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	safe := NewConfigService().GetSafe()

	if safe.Gravatar.ExtensionToken != "" {
		t.Fatalf("gravatar token 不应返回，实际 %q", safe.Gravatar.ExtensionToken)
	}
	if safe.RSS.ExtensionToken != "" {
		t.Fatalf("rss token 不应返回，实际 %q", safe.RSS.ExtensionToken)
	}
	// 但「有没有配」这个信息要保留，否则界面无法提示。
	if !safe.Gravatar.ExtensionTokenSet {
		t.Fatal("gravatar extension_token_set 应为 true")
	}
	if !safe.RSS.ExtensionTokenSet {
		t.Fatal("rss extension_token_set 应为 true")
	}

	// 整体序列化后再查一遍：万一哪天有人给结构体加了 json tag，
	// 这里能拦住。
	blob, err := json.Marshal(safe)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, secret := range []string{"super-secret-gravatar-token", "super-secret-rss-token"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("安全视图里出现了明文 token %q", secret)
		}
	}
}

func TestUpdateKeepsExtensionTokenWhenSubmittedEmpty(t *testing.T) {
	// 设置页拿到的是脱敏后的配置（token 为空串）。若后端把它当作用户意图
	// 直接写入，每保存一次设置都会把 token 抹掉，头像与抓取随即开始失败。
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	err := NewConfigService().Update(UpdateConfigRequest{
		Gravatar: &config.GravatarConfig{
			Enabled:      true,
			DefaultStyle: "mp",
			DefaultSize:  256,
			Rating:       "g",
			ExtensionURL: "http://ext:8090",
			// 前端回传空串
			ExtensionToken: "",
			TimeoutSeconds: 10,
			MaxBytes:       2097152,
		},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	gravatar := raw["gravatar"].(map[string]any)
	if got := gravatar["extension_token"]; got != "super-secret-gravatar-token" {
		t.Fatalf("空 token 提交后应保留原值，实际 %v", got)
	}
	if got := gravatar["default_style"]; got != "mp" {
		t.Fatalf("该改的字段没改：default_style = %v", got)
	}
}

func TestUpdatePreservesUnknownKeysInSection(t *testing.T) {
	// 设置页只认识结构体里的字段。整段替换会把 _comment 与用户自己加的
	// 说明清掉，而这些内容在 config.json 里是给人看的。
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	err := NewConfigService().Update(UpdateConfigRequest{
		Gravatar: &config.GravatarConfig{Enabled: false, DefaultStyle: "retro", DefaultSize: 128, Rating: "pg"},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	gravatar := raw["gravatar"].(map[string]any)
	if got := gravatar["_comment"]; got != "头像说明" {
		t.Fatalf("段内注释应被保留，实际 %v", got)
	}
	if got := raw["_comment"]; got != "顶层说明，保存设置时不应丢失" {
		t.Fatalf("顶层注释应被保留，实际 %v", got)
	}
}

func TestUpdateMergesProxyCredentialsByName(t *testing.T) {
	// SafeConfig 不返回代理地址，所以设置页回传的条目里 http_url 一定是空的。
	// 整体替换会让每次保存设置都清掉代理凭据，代理静默失效。
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	proxies := []config.ProxyEntry{{Name: "router", Type: "http", Enabled: false}}
	err := NewConfigService().Update(UpdateConfigRequest{Proxy: &proxies})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	list := raw["proxy"].([]any)
	if len(list) != 1 {
		t.Fatalf("代理条数应保持 1，实际 %d", len(list))
	}
	entry := list[0].(map[string]any)
	if got := entry["http_url"]; got != "http://127.0.0.1:7890" {
		t.Fatalf("代理地址被清空了，实际 %v", got)
	}
	if got := entry["enabled"]; got != false {
		t.Fatalf("enabled 应被更新为 false，实际 %v", got)
	}
}

func TestUpdateWritesExtensionHubSection(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	err := NewConfigService().Update(UpdateConfigRequest{
		ExtensionHub: &config.ExtensionHubConfig{
			Enabled:        true,
			BaseURL:        "https://ext.example.com/api/v1",
			PrivateKeyPath: "/etc/homehub/hub.key",
			TimeoutSeconds: 30,
		},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	hub := raw["extension_hub"].(map[string]any)
	if hub["enabled"] != true {
		t.Fatalf("enabled 应为 true，实际 %v", hub["enabled"])
	}
	if hub["base_url"] != "https://ext.example.com/api/v1" {
		t.Fatalf("base_url 未写入，实际 %v", hub["base_url"])
	}
}

func TestUpdateTouchesOnlyTheGivenSection(t *testing.T) {
	// 分段保存的前提：没提交的段完全不动。
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))
	before := readConfigJSON(t, path)

	err := NewConfigService().Update(UpdateConfigRequest{
		Log: &LogUpdate{Level: strPtr("debug")},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	after := readConfigJSON(t, path)
	if got := after["log"].(map[string]any)["level"]; got != "debug" {
		t.Fatalf("log.level 应为 debug，实际 %v", got)
	}
	// 其余段原样不动。
	for _, section := range []string{"gravatar", "rss", "proxy", "extension_hub"} {
		beforeBlob, _ := json.Marshal(before[section])
		afterBlob, _ := json.Marshal(after[section])
		if string(beforeBlob) != string(afterBlob) {
			t.Fatalf("%s 段被意外修改：\n before=%s\n after=%s", section, beforeBlob, afterBlob)
		}
	}
}

func strPtr(s string) *string { return &s }

// TestUpdateRejectsInvalidConfigWithoutTouchingTheFile 锁住「校验在写盘之前」。
//
// 顺序反过来的后果很严重：用户填错一个字段（比如 base_url 少了 http://），
// 保存返回错误，但 config.json 已经是无效的——重启服务就起不来，
// 而界面上只显示「保存失败」，没人会想到文件已经被改坏。
func TestUpdateRejectsInvalidConfigWithoutTouchingTheFile(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取原始配置失败: %v", err)
	}

	bad := &config.ExtensionHubConfig{
		Enabled:        true,
		BaseURL:        "ext.example.com/api/v1", // 少了 scheme
		PrivateKeyPath: "/etc/homehub/hub.key",
		TimeoutSeconds: 30,
	}
	updateErr := NewConfigService().Update(UpdateConfigRequest{ExtensionHub: bad})
	if updateErr == nil {
		t.Fatal("非法 base_url 必须被拒绝")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("校验失败时文件不应被修改\n before=%s\n after=%s", before, after)
	}

	// 原配置仍然可用：再加载一次应当成功。
	if err := config.Load(path); err != nil {
		t.Fatalf("拒绝写入后原配置应仍可加载: %v", err)
	}
}

// TestUpdateRejectsBaseURLWithoutVersionPrefix 确认缺 /api/v1 会被挡住。
//
// 少写前缀会让所有请求打到 /hub/* 之外，表现为一律 404——
// 而 404 在协议里又表示「RSS 模块未启用」，两个问题撞在一起极难分辨。
func TestUpdateRejectsBaseURLWithoutVersionPrefix(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	err := NewConfigService().Update(UpdateConfigRequest{
		ExtensionHub: &config.ExtensionHubConfig{
			Enabled:        true,
			BaseURL:        "https://ext.example.com",
			PrivateKeyPath: "/etc/homehub/hub.key",
			TimeoutSeconds: 30,
		},
	})
	if err == nil {
		t.Fatal("缺 /api/v1 的 base_url 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "配置未写入") {
		t.Fatalf("错误信息应说明文件未被修改，实际 %v", err)
	}
	_ = path
}
