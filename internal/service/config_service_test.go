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

// testPEM 是一把固定的 Ed25519 PKCS#8 私钥，测试里充当「已配置的密钥」。
// 每次生成会拖慢测试且让断言不稳定，所以写死一份。
const testPEM = "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIJ+DYVGC9YOnqHwWCUKGZ4bYyTCsVNzXHqZ3gkc0Xw8N\n-----END PRIVATE KEY-----\n"

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
		"timeout_seconds": 10,
		"max_bytes": 2097152
	},
	"rss": {
		"_comment": "RSS 说明",
		"background_fetch_enabled": true,
		"retention_count": 5000
	},
	"proxy": {
		"_comment": "代理说明",
		"entries": [
			{"name": "router", "type": "http", "http_url": "http://127.0.0.1:7890", "https_url": "http://127.0.0.1:7890", "enabled": true}
		],
		"default": ["router"]
	},
	"extension": {
		"_comment": "扩展说明",
		"enabled": false,
		"base_url": "",
		"private_key_pem": "super-secret-private-key",
		"timeout_seconds": 30
	}
}`

// TestSafeConfigNeverReturnsPrivateKey 是最重要的一条。
//
// 私钥是宿主的身份。一旦随 GET /config 下发到浏览器，它就同时存在于
// 开发者工具、前端内存和任何抓包里。这条断言防的就是这类回归。
func TestSafeConfigNeverReturnsPrivateKey(t *testing.T) {
	loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	safe := NewConfigService().GetSafe()

	// 结构体里根本没有这个字段，这里是编译期就能保证的；
	// 真正防的是「哪天有人为了方便加个 json 字段」。
	if !safe.Extension.PrivateKeySet {
		t.Fatal("extension.private_key_set 应为 true（配置里确实有一把）")
	}

	blob, err := json.Marshal(safe)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(blob), "super-secret-private-key") {
		t.Fatalf("安全视图里出现了私钥内容: %s", blob)
	}
}

// TestUpdateKeepsPrivateKeyWhenUpdatingOtherFields 锁住「改超时不该丢身份」。
//
// extension 段同时装着连接参数与私钥，而 Web 表单只知道前者。
// 整段替换会把 private_key_pem 抹掉，表现为保存一次设置后所有签名请求
// 变成 401，而界面上只显示「已保存」。
func TestUpdateKeepsPrivateKeyWhenUpdatingOtherFields(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	enabled := true
	timeout := 45
	err := NewConfigService().Update(UpdateConfigRequest{
		Extension: &ExtensionUpdate{
			Enabled:        &enabled,
			BaseURL:        strPtr("https://ext.example.com/api/v1"),
			TimeoutSeconds: &timeout,
		},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	ext := raw["extension"].(map[string]any)
	if got := ext["private_key_pem"]; got != "super-secret-private-key" {
		t.Fatalf("私钥应被保留，实际 %v", got)
	}
	if got := ext["timeout_seconds"]; got != float64(45) {
		t.Fatalf("该改的字段没改：timeout_seconds = %v", got)
	}
	if got := ext["enabled"]; got != true {
		t.Fatalf("enabled 应为 true，实际 %v", got)
	}
}

func strPtr(s string) *string { return &s }

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

	proxies := config.ProxyConfig{
		Entries: []config.ProxyEntry{{Name: "router", Type: "http", Enabled: false}},
	}
	err := NewConfigService().Update(UpdateConfigRequest{Proxy: &proxies})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	section := raw["proxy"].(map[string]any)
	list := section["entries"].([]any)
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
	// 请求里没带 default，应保留原链而不是被清空。
	def, ok := section["default"].([]any)
	if !ok || len(def) != 1 || def[0] != "router" {
		t.Fatalf("default 代理链应被保留，实际 %v", section["default"])
	}
}

func TestUpdateWritesExtensionSection(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	enabled := true
	timeout := 30
	err := NewConfigService().Update(UpdateConfigRequest{
		Extension: &ExtensionUpdate{
			Enabled:        &enabled,
			BaseURL:        strPtr("https://ext.example.com/api/v1"),
			TimeoutSeconds: &timeout,
		},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	ext := raw["extension"].(map[string]any)
	if ext["enabled"] != true {
		t.Fatalf("enabled 应为 true，实际 %v", ext["enabled"])
	}
	if ext["base_url"] != "https://ext.example.com/api/v1" {
		t.Fatalf("base_url 未写入，实际 %v", ext["base_url"])
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

	enabled := true
	timeout := 30
	bad := &ExtensionUpdate{
		Enabled:        &enabled,
		BaseURL:        strPtr("ext.example.com/api/v1"), // 少了 scheme
		TimeoutSeconds: &timeout,
	}
	updateErr := NewConfigService().Update(UpdateConfigRequest{Extension: bad})
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

	enabled := true
	timeout := 30
	err := NewConfigService().Update(UpdateConfigRequest{
		Extension: &ExtensionUpdate{
			Enabled:        &enabled,
			BaseURL:        strPtr("https://ext.example.com"),
			TimeoutSeconds: &timeout,
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

// TestUpdatePersistsRetentionUnlimited 确认设置页能关掉无限存储并落盘。
//
// 无限存储默认开启，所以"关掉"必须是显式写 false 才能覆盖默认值。
// 若保存路径丢掉这个字段，重新加载后又会回到 true——界面上会表现为
// "保存了但重启后又变回无限"，这是最难解释的一类 bug。
func TestUpdatePersistsRetentionUnlimited(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	err := NewConfigService().Update(UpdateConfigRequest{
		RSS: &config.RSSConfig{
			RetentionUnlimited:        false,
			RetentionCount:            1234,
			BackgroundFetchEnabled:    true,
			BackgroundFetchInterval:   300,
			FetchConcurrency:          4,
			FetchTimeoutSeconds:       30,
			ImageCacheEnabled:         true,
			ImageCacheConcurrency:     6,
			ImageMaxBytes:             128 * 1024 * 1024,
			ImageCleanupEnabled:       true,
			ImageRetentionDays:        30,
			ImageCleanupIntervalHours: 24,
		},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	raw := readConfigJSON(t, path)
	rss := raw["rss"].(map[string]any)
	if got, ok := rss["retention_unlimited"].(bool); !ok || got {
		t.Fatalf("retention_unlimited 应写为 false，实际 %v", rss["retention_unlimited"])
	}
	if got := rss["retention_count"].(float64); got != 1234 {
		t.Fatalf("retention_count = %v, want 1234", got)
	}

	// 重新加载后仍应是关闭状态，而不是被默认值覆盖回 true。
	if config.Get().RSS.RetentionUnlimited {
		t.Fatal("重新加载后无限存储不该被默认值重新打开")
	}
}

// TestUpdateWritesLogRetentionDays 确认设置页能改日志保留期，且 0 能落盘。
//
// LogUpdate 是逐字段白名单；新字段不登记就会被静默丢弃，界面显示
// 「已保存」而文件没变。0（不限）是最容易丢的值。
func TestUpdateWritesLogRetentionDays(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	zero := 0
	if err := NewConfigService().Update(UpdateConfigRequest{
		Log: &LogUpdate{RetentionDays: &zero},
	}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	after := readConfigJSON(t, path)
	if got, ok := after["log"].(map[string]any)["retention_days"]; !ok || got != float64(0) {
		t.Fatalf("log.retention_days 应写入 0，实际 %v (存在=%v)", got, ok)
	}
	if got := config.Get().Log.RetentionDays; got != 0 {
		t.Fatalf("热加载后 RetentionDays = %d，期望 0", got)
	}
}
