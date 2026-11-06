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

// TestGetSafeBalanceMonitorOmitsTargets 确认设置页能读到余额监控开关，
// 但监控点（含 api_key）绝不随配置视图下发。
//
// 防的回归：有人图省事把 config.BalanceMonitorConfig 整个塞进 SafeConfig，
// 遗留在 config.json 里的 targets[].api_key 就会出现在 GET /config 响应里。
func TestGetSafeBalanceMonitorOmitsTargets(t *testing.T) {
	base := strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1)
	base = strings.Replace(base, `"log": {`,
		`"balance_monitor": {"enabled": true, "refresh_interval_seconds": 120, "allow_private_hosts": true,
			"targets": [{"name": "ds", "type": "deepseek", "base_url": "https://api.example.com", "api_key": "LEAKMARKER"}]},
	"log": {`, 1)
	loadSettingsConfig(t, base)

	safe := NewConfigService().GetSafe()
	if !safe.BalanceMonitor.Enabled || safe.BalanceMonitor.RefreshIntervalSeconds != 120 || !safe.BalanceMonitor.AllowPrivateHosts {
		t.Fatalf("余额监控视图字段不对: %+v", safe.BalanceMonitor)
	}
	out, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "LEAKMARKER") || strings.Contains(string(out), "targets") {
		t.Fatalf("配置视图泄露了监控点: %s", out)
	}
}

// TestUpdateBalanceMonitorMergesFields 确认只改传入的字段，且 targets 保留。
//
// 整段替换会把 config.json 里遗留的 targets 清掉；它们虽已迁入数据库，
// 但「保存一个开关就删掉别的配置」不是用户能预期的行为。
func TestUpdateBalanceMonitorMergesFields(t *testing.T) {
	base := strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1)
	base = strings.Replace(base, `"log": {`,
		`"balance_monitor": {"enabled": true, "refresh_interval_seconds": 120,
			"targets": [{"name": "ds", "type": "deepseek", "base_url": "https://api.example.com", "api_key": "sk-keep"}]},
	"log": {`, 1)
	path := loadSettingsConfig(t, base)

	allow := true
	if err := NewConfigService().Update(UpdateConfigRequest{
		BalanceMonitor: &BalanceMonitorUpdate{AllowPrivateHosts: &allow},
	}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	bm := readConfigJSON(t, path)["balance_monitor"].(map[string]any)
	if bm["allow_private_hosts"] != true {
		t.Fatalf("allow_private_hosts 未写入: %v", bm)
	}
	if bm["refresh_interval_seconds"] != float64(120) || bm["enabled"] != true {
		t.Fatalf("未传入的字段被改动: %v", bm)
	}
	if targets, ok := bm["targets"].([]any); !ok || len(targets) != 1 {
		t.Fatalf("targets 被清掉了: %v", bm["targets"])
	}
	if !config.Get().BalanceMonitor.AllowPrivateHosts {
		t.Fatal("热加载后 AllowPrivateHosts 应为 true")
	}
}

// TestUpdateServerTrustedProxies 确认可信代理能写入，非法值在落盘前被拒。
func TestUpdateServerTrustedProxies(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))
	svc := NewConfigService()

	good := []string{"10.0.0.1", "172.16.0.0/12"}
	if err := svc.Update(UpdateConfigRequest{Server: &ServerUpdate{TrustedProxies: &good}}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got := readConfigJSON(t, path)["server"].(map[string]any)["trusted_proxies"].([]any)
	if len(got) != 2 || got[1] != "172.16.0.0/12" {
		t.Fatalf("trusted_proxies 写入不对: %v", got)
	}

	bad := []string{"not-an-ip"}
	if err := svc.Update(UpdateConfigRequest{Server: &ServerUpdate{TrustedProxies: &bad}}); err == nil {
		t.Fatal("非法代理地址应被拒绝")
	}
	again := readConfigJSON(t, path)["server"].(map[string]any)["trusted_proxies"].([]any)
	if len(again) != 2 {
		t.Fatalf("校验失败后文件不该被改动: %v", again)
	}
}

// TestGetSafeMasksPikPakCredentials 确认 PikPak 账号密码与 client_secret 不随配置视图下发。
//
// 防的回归：SafeConfig.RSS 曾直接取 config.RSSConfig，嵌套的 pikpak 段
// 原样带出了明文密码。数据库密码与私钥早已脱敏，这一处是漏网的。
func TestGetSafeMasksPikPakCredentials(t *testing.T) {
	base := strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1)
	base = strings.Replace(base, `"retention_count": 5000`,
		`"retention_count": 5000,
		"pikpak": {"enabled": true, "username": "u@example.com", "password": "PIKPAKPASS", "client_id": "cid", "client_secret": "PIKPAKSECRET"}`, 1)
	loadSettingsConfig(t, base)

	safe := NewConfigService().GetSafe()
	out, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "PIKPAKPASS") || strings.Contains(string(out), "PIKPAKSECRET") {
		t.Fatalf("配置视图泄露了 PikPak 凭据: %s", out)
	}
	if !safe.RSS.PikPak.PasswordSet || !safe.RSS.PikPak.ClientSecretSet {
		t.Fatalf("应标记凭据已设置: %+v", safe.RSS.PikPak)
	}
	if safe.RSS.PikPak.Username != "u@example.com" || safe.RSS.PikPak.ClientID != "cid" {
		t.Fatalf("非机密字段应保留: %+v", safe.RSS.PikPak)
	}
}

// TestUpdateRSSKeepsPikPakSecretsWhenBlank 确认回传脱敏后的视图不会抹掉凭据。
//
// 视图里 password 为空串；JSON 编辑模式会把整段 rss（含 pikpak）原样
// 提交回来。若按值写回，保存一次设置就清空了 PikPak 密码。
func TestUpdateRSSKeepsPikPakSecretsWhenBlank(t *testing.T) {
	base := strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1)
	base = strings.Replace(base, `"retention_count": 5000`,
		`"retention_count": 5000,
		"pikpak": {"enabled": true, "username": "u", "password": "keep-pass", "client_id": "cid", "client_secret": "keep-secret"}`, 1)
	path := loadSettingsConfig(t, base)

	rss := config.Get().RSS
	rss.PikPak.Password = ""
	rss.PikPak.ClientSecret = ""
	rss.PikPak.Username = "u2"
	if err := NewConfigService().Update(UpdateConfigRequest{RSS: &rss}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	pk := readConfigJSON(t, path)["rss"].(map[string]any)["pikpak"].(map[string]any)
	if pk["password"] != "keep-pass" || pk["client_secret"] != "keep-secret" {
		t.Fatalf("空值不应覆盖已有凭据: %v", pk)
	}
	if pk["username"] != "u2" {
		t.Fatalf("非机密字段应正常更新: %v", pk)
	}
}

// TestUpdatePersistsExtensionFeaturesAndReturnsThem 防的是「Extension 开关
// 形同虚设」：设置页会提交 features.hub/fetch/avatar，但 ExtensionUpdate
// 曾经没有这个字段，JSON 解码时被静默丢弃；GET 也不下发它，前端只能
// 回落到 false。结果勾上「启用」后状态页仍报 disabled（Status 要求
// enabled && features.hub），刷新后三个勾选框又全部变回未选中。
func TestUpdatePersistsExtensionFeaturesAndReturnsThem(t *testing.T) {
	path := loadSettingsConfig(t, strings.Replace(settingsBaseConfig, "%s", strings.Repeat("a", 32), 1))

	var req UpdateConfigRequest
	// 走 JSON 解码而不是直接构造结构体：回归发生在解码阶段（未知字段被丢）。
	body := `{"extension": {"enabled": true, "base_url": "https://ext.example.com/api/v1",
		"features": {"hub": true, "fetch": false, "avatar": true}}}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("解码请求失败: %v", err)
	}
	if err := NewConfigService().Update(req); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	ext := readConfigJSON(t, path)["extension"].(map[string]any)
	features, ok := ext["features"].(map[string]any)
	if !ok {
		t.Fatalf("config.json 里没有写入 extension.features：%v", ext)
	}
	if features["hub"] != true || features["fetch"] != false || features["avatar"] != true {
		t.Fatalf("features 写入不对：%v", features)
	}
	if ext["private_key_pem"] != "super-secret-private-key" {
		t.Fatalf("更新 features 不应碰私钥，实际 %v", ext["private_key_pem"])
	}

	safe := NewConfigService().GetSafe()
	if !safe.Extension.Features.Hub || safe.Extension.Features.Fetch || !safe.Extension.Features.Avatar {
		t.Fatalf("GET /config 应下发已保存的 features，实际 %+v", safe.Extension.Features)
	}
}

// TestUpdateHomeBackgroundKeepsLoginPage 防的是设置页「背景与外观」保存后
// 登录页配置被抹掉。前端只提交 webui.home.background，后端曾用整段替换
// raw["webui"]：login.layout 与 login.background 变成零值（mode=""、
// interval=0），配置随即校验失败或登录页布局被重置。
func TestUpdateHomeBackgroundKeepsLoginPage(t *testing.T) {
	body := strings.Replace(settingsBaseConfig, `"gravatar": {`, `"webui": {
		"home": {"background": {"mode": "single", "interval_seconds": 30, "transition_effect": "fade", "fade_duration_ms": 1000, "selected_asset_ids": []}},
		"login": {"layout": "left", "background": {"mode": "random", "interval_seconds": 45, "transition_effect": "slide", "fade_duration_ms": 800, "selected_asset_ids": [7]}}
	},
	"gravatar": {`, 1)
	path := loadSettingsConfig(t, strings.Replace(body, "%s", strings.Repeat("a", 32), 1))

	var req UpdateConfigRequest
	if err := json.Unmarshal([]byte(`{"webui":{"home":{"background":{"mode":"carousel","interval_seconds":10,"transition_effect":"fade","fade_duration_ms":500,"selected_asset_ids":[1,2]}}}}`), &req); err != nil {
		t.Fatal(err)
	}
	if err := NewConfigService().Update(req); err != nil {
		t.Fatalf("只改首页背景不应失败: %v", err)
	}

	webui := readConfigJSON(t, path)["webui"].(map[string]any)
	login := webui["login"].(map[string]any)
	if login["layout"] != "left" {
		t.Fatalf("login.layout 被改成 %v", login["layout"])
	}
	if bg := login["background"].(map[string]any); bg["mode"] != "random" || bg["interval_seconds"] != float64(45) {
		t.Fatalf("login.background 被改动: %v", bg)
	}
	if home := webui["home"].(map[string]any)["background"].(map[string]any); home["mode"] != "carousel" {
		t.Fatalf("home.background 未写入: %v", home)
	}
}
