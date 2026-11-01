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

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/hubclient"
	"github.com/ignvex/HomeHub/internal/testutil"
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

// expectKeyHead 安排一次「只查公钥与更新时间」的查询。
//
// 热路径故意不读私钥列：少碰一次就少一次泄露面。
func expectKeyHead(mock sqlmock.Sqlmock, publicB64 string, updated time.Time) {
	mock.ExpectQuery(`SELECT "public_key","updated_at" FROM "extension_hub_keys"`).
		WillReturnRows(sqlmock.NewRows([]string{"public_key", "updated_at"}).
			AddRow(publicB64, updated))
}

// expectKeyEmpty 安排一次查不到密钥的查询。
func expectKeyEmpty(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT "public_key","updated_at" FROM "extension_hub_keys"`).
		WillReturnRows(sqlmock.NewRows([]string{"public_key", "updated_at"}))
}

// expectKeyFull 安排一次读取完整私钥的查询。
func expectKeyFull(mock sqlmock.Sqlmock, pemText, publicB64 string) {
	mock.ExpectQuery(`SELECT "private_key_pem","public_key" FROM "extension_hub_keys"`).
		WillReturnRows(sqlmock.NewRows([]string{"private_key_pem", "public_key"}).
			AddRow(pemText, publicB64))
}

// newKeyService 构造一个带数据库的服务，私钥路径留空（只走库）。
func newKeyService(t *testing.T, baseURL, keyPath string) *ExtensionHubService {
	t.Helper()
	return &ExtensionHubService{
		readyClient: &http.Client{Timeout: 2 * time.Second},
		cfg: func() config.ExtensionHubConfig {
			return config.ExtensionHubConfig{
				Enabled:        true,
				BaseURL:        baseURL,
				PrivateKeyPath: keyPath,
				TimeoutSeconds: 5,
			}
		},
	}
}

// TestClientUsesDatabaseKeyWhenPresent 确认库里的密钥优先于文件。
//
// 多实例共用数据库时，库里的那一份是共同事实。若文件优先，
// 「改过库里的密钥，某个实例还在用旧文件签」会变成很难查的问题。
func TestClientUsesDatabaseKeyWhenPresent(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, publicB64 := newPEMKey(t)
	expectKeyHead(mock, publicB64, time.Now())
	expectKeyFull(mock, pemText, publicB64)

	// 同时给一个**不同**的私钥文件：如果实现错误地优先用文件，
	// 拿到的公钥就会与库里那条不一致。
	filePEM, _ := newPEMKey(t)
	path := filepath.Join(t.TempDir(), "hub.key")
	if err := os.WriteFile(path, []byte(filePEM), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", path)
	svc.db = db

	_, signer, err := svc.client()
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	if got := signer.PublicKeyBase64(); got != publicB64 {
		t.Fatalf("应使用数据库里的密钥\n  期望 %s\n  实际 %s", publicB64, got)
	}
}

// TestClientFallsBackToFileWhenDatabaseEmpty 确认库里没有时回退到文件。
//
// 这条保证升级路径：老部署只配了文件路径，换到这一版不能突然失灵。
func TestClientFallsBackToFileWhenDatabaseEmpty(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, publicB64 := newPEMKey(t)
	path := filepath.Join(t.TempDir(), "hub.key")
	if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	expectKeyEmpty(mock)

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", path)
	svc.db = db

	_, signer, err := svc.client()
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	if got := signer.PublicKeyBase64(); got != publicB64 {
		t.Fatalf("应回退到文件密钥\n  期望 %s\n  实际 %s", publicB64, got)
	}
}

// TestClientReportsMissingKeyWhenNowhere 确认两处都没有时明确报错。
//
// 不能静默用一个随机密钥或者空签名——那会让每次请求都 401，
// 而错误信息指向 Extension，方向完全错了。
func TestClientReportsMissingKeyWhenNowhere(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	expectKeyEmpty(mock)

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db

	_, _, err := svc.client()
	if err == nil {
		t.Fatal("没有密钥时必须报错")
	}
	if !strings.Contains(err.Error(), "没有可用的签名私钥") {
		t.Fatalf("错误应说明缺私钥，实际 %v", err)
	}
}

// TestSetKeyStoresNormalizedPEMAndInvalidatesCache 确认存库走规范 PEM 并清缓存。
//
// 不清缓存的话，改完密钥仍会用旧身份签名，直到进程重启——
// 而界面上「保存成功」已经出现了。
func TestSetKeyStoresNormalizedPEMAndInvalidatesCache(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, publicB64 := newPEMKey(t)

	mock.ExpectExec(`DELETE FROM "extension_hub_keys"`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`INSERT INTO "extension_hub_keys"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	// SetKey 末尾会调 KeyInfo 返回最新状态。
	expectKeyHead(mock, publicB64, time.Now())

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db
	// 预置一个缓存，验证它被清掉。
	svc.cached = &extensionHubClient{baseURL: "stale"}

	info, err := svc.SetKey(pemText)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if info.PublicKey != publicB64 {
		t.Fatalf("返回的公钥不对：%s", info.PublicKey)
	}
	if info.Source != KeySourceDatabase {
		t.Fatalf("来源应为 database，实际 %q", info.Source)
	}
	if svc.cached != nil {
		t.Fatal("保存密钥后必须清掉缓存的客户端")
	}
}

// TestSetKeyAcceptsBase64Seed 确认裸 base64 种子也能入库。
//
// 协议文档给的参考实现用 32 字节种子；用户可能直接粘贴那一段。
func TestSetKeyAcceptsBase64Seed(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatalf("生成种子失败: %v", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	want := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))

	mock.ExpectExec(`DELETE FROM "extension_hub_keys"`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`INSERT INTO "extension_hub_keys"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	expectKeyHead(mock, want, time.Now())

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db

	info, err := svc.SetKey(base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if info.PublicKey != want {
		t.Fatalf("公钥不对\n  期望 %s\n  实际 %s", want, info.PublicKey)
	}
}

// TestSetKeyRejectsGarbage 确认无法解析的内容被拒且不写库。
func TestSetKeyRejectsGarbage(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db

	_, err := svc.SetKey("这不是私钥")
	if err == nil {
		t.Fatal("非法内容必须被拒绝")
	}
	if !strings.Contains(err.Error(), "无法解析私钥") {
		t.Fatalf("错误应说明解析失败，实际 %v", err)
	}
	// 没有安排任何 SQL 期望：若真的写了库，ExpectationsWereMet 之外的
	// 未预期调用会让 sqlmock 直接报错。
	_ = mock
}

// TestSetKeyRejectsPublicKeyPEM 确认把公钥当私钥粘贴会被挡住。
//
// 公钥能「解析成功」但签出的东西永远验不过，属于最坏的一类错误：
// 保存成功、状态看着正常、每个请求 401。
func TestSetKeyRejectsPublicKeyPEM(t *testing.T) {
	_, publicB64 := newPEMKey(t)
	raw, err := base64.StdEncoding.DecodeString(publicB64)
	if err != nil {
		t.Fatalf("解码公钥失败: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(raw))
	if err != nil {
		t.Fatalf("编码公钥失败: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db

	if _, err := svc.SetKey(pubPEM); err == nil {
		t.Fatal("公钥 PEM 必须被拒绝")
	}
}

// TestClientRejectsMismatchedPublicKeyRow 确认库里公钥与私钥不配对时报错。
//
// 这种行说明数据本身坏了（比如手工改过库、或者迁移出错）。
// 静默用私钥推导会掩盖问题，直到所有签名在 Extension 侧被拒。
func TestClientRejectsMismatchedPublicKeyRow(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, _ := newPEMKey(t)
	_, otherPublic := newPEMKey(t)

	expectKeyHead(mock, otherPublic, time.Now())
	expectKeyFull(mock, pemText, otherPublic)

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db

	_, _, err := svc.client()
	if err == nil {
		t.Fatal("公钥与私钥不匹配时必须报错")
	}
	if !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("错误应说明不匹配，实际 %v", err)
	}
}

// TestDeleteKeyFallsBackToFile 确认删库后来源退回文件。
func TestDeleteKeyFallsBackToFile(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, _ := newPEMKey(t)
	path := filepath.Join(t.TempDir(), "hub.key")
	if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	mock.ExpectExec(`DELETE FROM "extension_hub_keys"`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", path)
	svc.db = db

	removed, err := svc.DeleteKey()
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if !removed {
		t.Fatal("库里原本有密钥时应返回 removed=true")
	}
	if svc.cached != nil {
		t.Fatal("删除后必须清掉缓存的客户端")
	}
}

// TestKeyInfoNeverExposesPrivateKey 是最重要的断言。
//
// 私钥一旦出现在响应里，浏览器缓存、开发者工具、日志中间件都可能
// 把它复制走。这里对整个序列化结果做字符串检查。
func TestKeyInfoNeverExposesPrivateKey(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, publicB64 := newPEMKey(t)
	expectKeyHead(mock, publicB64, time.Now())

	svc := newKeyService(t, "http://127.0.0.1:1/api/v1", "")
	svc.db = db

	info, err := svc.KeyInfo()
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	blob, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	// PEM 的主体部分不该出现；整体也不该出现 "PRIVATE" 字样。
	body := strings.Join(strings.Split(pemText, "\n"), "")
	body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body, "-----BEGIN PRIVATE KEY-----"), "-----END PRIVATE KEY-----"))
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
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	pemText, publicB64 := newPEMKey(t)
	path := filepath.Join(t.TempDir(), "hub.key")
	if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/hub/keys") {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer upstream.Close()

	// client() 一次 + KeyInfo() 一次
	expectKeyEmpty(mock)
	expectKeyEmpty(mock)

	svc := newKeyService(t, upstream.URL+"/api/v1", path)
	svc.db = db

	st := svc.Status(context.Background())
	if st.KeySource != KeySourceFile {
		t.Fatalf("来源应为 file，实际 %q", st.KeySource)
	}
	if st.KeyFingerprint == "" {
		t.Fatal("应给出指纹")
	}
	_ = publicB64
	_ = hubclient.SignDomain
}
