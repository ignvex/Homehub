package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/hubclient"
)

// newPEMKey 生成一把 PKCS#8 PEM 私钥，返回 PEM 文本与公钥 base64。
func newPEMKey(t *testing.T) (pemText, publicB64 string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if block == nil {
		t.Fatal("生成 PEM 失败")
	}
	return string(block), base64.StdEncoding.EncodeToString(pub)
}

// newKeyService 构造一个指向固定配置的服务，不落数据库。
func newKeyService(t *testing.T, baseURL, pemText string) *ExtensionHubService {
	t.Helper()
	return &ExtensionHubService{
		readyClient: &http.Client{Timeout: 2 * time.Second},
		cfg: func() config.ExtensionConfig {
			return config.ExtensionConfig{
				Enabled:        true,
				BaseURL:        baseURL,
				PrivateKeyPEM:  pemText,
				TimeoutSeconds: 5,
				Features:       config.ExtensionFeatures{Hub: true, Fetch: true, Avatar: true},
			}
		},
	}
}

// writeKeyConfig 写一份最小配置并加载，返回路径。
//
// SetKey / DeleteKey 会改写 config.Path() 指向的文件，所以测试必须先
// 把 loadedPath 指到临时文件上，否则会动到仓库里的真实配置。
func writeKeyConfig(t *testing.T, pemText string, enabled bool) string {
	t.Helper()

	pemJSON, err := json.Marshal(pemText)
	if err != nil {
		t.Fatalf("序列化 PEM 失败: %v", err)
	}

	enabledLit := "false"
	if enabled {
		enabledLit = "true"
	}

	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d", "sslmode": "disable"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"extension": {
			"enabled": ` + enabledLit + `,
			"base_url": "https://ext.example.com/api/v1",
			"private_key_pem": ` + string(pemJSON) + `,
			"timeout_seconds": 30
		}
	}`

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return path
}

// readExtensionPEM 读回 config.json 里配置的私钥。
func readExtensionPEM(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	section, _ := raw["extension"].(map[string]any)
	if section == nil {
		return ""
	}
	s, _ := section["private_key_pem"].(string)
	return s
}

// TestClientRejectsMissingKey 确认没有私钥时给出可执行的错误。
//
// 「未配置」与「配了但解析失败」是两种修法：前者要粘贴一把，
// 后者要检查粘贴内容是否被截断。错误信息必须能让读者区分。
func TestClientRejectsMissingKey(t *testing.T) {
	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")

	_, _, err := svc.client()
	if err == nil {
		t.Fatal("没有私钥时必须报错")
	}
	if !strings.Contains(err.Error(), "private_key_pem") {
		t.Fatalf("错误应指向配置项，实际 %v", err)
	}
}

// TestClientRejectsBrokenPEM 确认损坏的私钥不会被静默忽略。
func TestClientRejectsBrokenPEM(t *testing.T) {
	svc := newKeyService(t, "http://127.0.0.1:1/api/v1",
		"-----BEGIN PRIVATE KEY-----\nnot-base64\n-----END PRIVATE KEY-----\n")

	_, _, err := svc.client()
	if err == nil {
		t.Fatal("损坏的私钥必须报错")
	}
	if !strings.Contains(err.Error(), "private_key_pem") {
		t.Fatalf("错误应指向配置项，实际 %v", err)
	}
}

// TestClientRebuildsWhenKeyRotates 锁住「轮换密钥后必须重签」。
//
// 只按 baseURL 缓存客户端会让轮换失效：地址没变、私钥变了，
// 结果是一直用旧身份签出 401，且没有任何提示。
func TestClientRebuildsWhenKeyRotates(t *testing.T) {
	pemA, pubA := newPEMKey(t)

	current := config.ExtensionConfig{
		Enabled:        true,
		BaseURL:        "http://127.0.0.1:1/api/v1",
		PrivateKeyPEM:  pemA,
		TimeoutSeconds: 5,
		Features:       config.ExtensionFeatures{Hub: true},
	}
	svc := &ExtensionHubService{
		readyClient: &http.Client{Timeout: 2 * time.Second},
		cfg:         func() config.ExtensionConfig { return current },
	}

	_, signerA, err := svc.client()
	if err != nil {
		t.Fatalf("首次构造失败: %v", err)
	}
	if signerA.PublicKeyBase64() != pubA {
		t.Fatalf("公钥不符: %s", signerA.PublicKeyBase64())
	}

	// 换一把私钥，其余配置不变。
	current.PrivateKeyPEM, _ = newPEMKey(t)
	_, signerB, err := svc.client()
	if err != nil {
		t.Fatalf("轮换后构造失败: %v", err)
	}
	if signerB.PublicKeyBase64() == pubA {
		t.Fatal("轮换私钥后仍在使用旧身份签名")
	}
}

// TestSetKeyWritesConfigFile 确认私钥写进 config.json 且被规范化成 PEM。
func TestSetKeyWritesConfigFile(t *testing.T) {
	path := writeKeyConfig(t, "", false)
	pemText, publicB64 := newPEMKey(t)

	// 用裸 32 字节种子的 base64 形式提交，确认落盘的是规范化 PEM。
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		t.Fatal("测试私钥不是合法 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("解析私钥失败: %v", err)
	}
	seedB64 := base64.StdEncoding.EncodeToString(parsed.(ed25519.PrivateKey).Seed())

	// SetKey 会写 config.Path() 并触发重载，所以必须用读全局配置的实例，
	// 否则写完自己也读不到新值。
	svc := NewExtensionHubService(nil)
	info, err := svc.SetKey(seedB64)
	if err != nil {
		t.Fatalf("设置失败: %v", err)
	}
	if info.PublicKey != publicB64 {
		t.Fatalf("公钥不符: %s", info.PublicKey)
	}

	stored := readExtensionPEM(t, path)
	if !strings.HasPrefix(stored, "-----BEGIN PRIVATE KEY-----") {
		t.Fatalf("落盘内容应是 PEM，实际 %q", firstLine(stored))
	}
	signer, err := hubclient.LoadSignerFromBytes([]byte(stored))
	if err != nil {
		t.Fatalf("落盘的 PEM 无法解析: %v", err)
	}
	if signer.PublicKeyBase64() != publicB64 {
		t.Fatal("落盘私钥与提交的不一致")
	}
}

// TestSetKeyRejectsGarbage 确认垃圾输入被拒绝且文件未被改动。
func TestSetKeyRejectsGarbage(t *testing.T) {
	path := writeKeyConfig(t, "original", false)
	before := readExtensionPEM(t, path)

	svc := NewExtensionHubService(nil)
	if _, err := svc.SetKey("这不是私钥"); err == nil {
		t.Fatal("垃圾输入必须被拒绝")
	}

	if after := readExtensionPEM(t, path); after != before {
		t.Fatalf("校验失败时不应改动文件，实际 %q", after)
	}
}

// TestSetKeyRejectsPublicKeyPEM 确认贴错公钥时给出明确拒绝。
func TestSetKeyRejectsPublicKeyPEM(t *testing.T) {
	writeKeyConfig(t, "", false)

	_, publicB64 := newPEMKey(t)
	pubRaw, _ := base64.StdEncoding.DecodeString(publicB64)
	der, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(pubRaw))
	if err != nil {
		t.Fatalf("编码公钥失败: %v", err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	svc := NewExtensionHubService(nil)
	if _, err := svc.SetKey(publicPEM); err == nil {
		t.Fatal("公钥必须被拒绝")
	}
}

// TestDeleteKeyRefusesWhileEnabled 确认清除私钥前必须先关开关。
//
// 否则会写出一份 enabled=true 但没有私钥的配置，被校验拦下时
// 错误信息指向文件而不是「你还开着开关」。
func TestDeleteKeyRefusesWhileEnabled(t *testing.T) {
	pemText, _ := newPEMKey(t)
	path := writeKeyConfig(t, pemText, true)
	before := readExtensionPEM(t, path)

	svc := NewExtensionHubService(nil)
	if _, err := svc.DeleteKey(); err == nil {
		t.Fatal("enabled=true 时应拒绝清除私钥")
	}
	if after := readExtensionPEM(t, path); after != before {
		t.Fatal("拒绝时不应改动文件")
	}
}

// TestDeleteKeyClearsWhenDisabled 确认关闭后可以清除。
func TestDeleteKeyClearsWhenDisabled(t *testing.T) {
	pemText, _ := newPEMKey(t)
	path := writeKeyConfig(t, pemText, false)

	svc := NewExtensionHubService(nil)
	removed, err := svc.DeleteKey()
	if err != nil {
		t.Fatalf("清除失败: %v", err)
	}
	if !removed {
		t.Fatal("应报告已移除")
	}
	if got := readExtensionPEM(t, path); got != "" {
		t.Fatalf("私钥应被清空，实际 %q", firstLine(got))
	}
}

// TestKeyInfoNeverExposesPrivateKey 是最重要的一条。
func TestKeyInfoNeverExposesPrivateKey(t *testing.T) {
	pemText, publicB64 := newPEMKey(t)
	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", pemText)

	info, err := svc.KeyInfo()
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	blob, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	body := strings.ReplaceAll(pemText, "\n", "")
	body = strings.TrimPrefix(body, "-----BEGIN PRIVATE KEY-----")
	body = strings.TrimSuffix(body, "-----END PRIVATE KEY-----")
	body = strings.TrimSpace(body)
	if body != "" && strings.Contains(string(blob), body) {
		t.Fatal("响应里出现了私钥内容")
	}
	if strings.Contains(string(blob), "PRIVATE") {
		t.Fatalf("响应里出现了 PRIVATE 字样: %s", blob)
	}
	if info.PublicKey != publicB64 {
		t.Fatalf("公钥应可读，实际 %q", info.PublicKey)
	}
	if info.Fingerprint == "" {
		t.Fatal("应给出指纹便于核对")
	}
}

// TestStatusReportsKeySource 确认状态接口能说明密钥来自哪里。
func TestStatusReportsKeySource(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer upstream.Close()

	pemText, _ := newPEMKey(t)
	svc := newKeyService(t, upstream.URL+"/api/v1", pemText)

	st := svc.Status(context.Background())
	if st.KeySource != KeySourceConfig {
		t.Fatalf("来源应为 config，实际 %q", st.KeySource)
	}
	if st.KeyFingerprint == "" {
		t.Fatal("应给出指纹")
	}
}

// TestKeyInfoProvidesPEMForDeployment 确认返回可直接粘贴的 PEM。
//
// 界面上原先给的是裸 32 字节公钥的 base64。协议文档说 Extension「接受
// PKIX PEM、裸 base64 或文件路径」，但没说明「裸 base64」指的是 32 字节
// 还是 DER 的 base64 —— 两者差 12 字节 ASN.1 头，选错会被当成无效公钥。
// PEM 自带类型标记，没有这个歧义，所以部署路径上应该给 PEM。
func TestKeyInfoProvidesPEMForDeployment(t *testing.T) {
	pemText, _ := newPEMKey(t)
	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", pemText)

	info, err := svc.KeyInfo()
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	out := info.PublicKeyPEM
	if out == "" {
		t.Fatal("应给出 PEM 形式的公钥")
	}
	if !strings.HasPrefix(out, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("PEM 头不对: %q", firstLine(out))
	}
}

// TestKeyInfoReportsBrokenPEM 确认「配了但解析不了」与「未配置」分开。
func TestKeyInfoReportsBrokenPEM(t *testing.T) {
	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "not-a-key")

	info, err := svc.KeyInfo()
	if err != nil {
		t.Fatalf("查询不应报错: %v", err)
	}
	if !info.Broken {
		t.Fatal("应标记为 Broken")
	}
	if info.Configured {
		t.Fatal("解析失败不应算作已配置")
	}
}

// TestFingerprintIsStable 确认同一输入得到同一指纹。
func TestFingerprintIsStable(t *testing.T) {
	_, publicB64 := newPEMKey(t)

	if fingerprintKey(publicB64) != fingerprintKey(publicB64) {
		t.Fatal("同一输入应得到同一指纹")
	}
	if fingerprintKey(publicB64) == "" {
		t.Fatal("指纹不应为空")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 40 {
		return s[:40]
	}
	return s
}
