package handler

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// avatarTestPNG 生成一张合法的 1x1 PNG。
func avatarTestPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("生成 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// loadAvatarConfig 写入并加载一份最小配置，gravatar 段可追加字段。
func loadAvatarConfig(t *testing.T, extra string) {
	t.Helper()

	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "error", "format": "json"},
		"gravatar": {"enabled": true, "default_size": 256, "default_style": "identicon", "rating": "g"` + extra + `}
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
}


// loadAvatarConfigWithExtension 配置一份指向 upstream 的 Extension 代理。
//
// 旧版只需填 gravatar.extension_url；重构后连接与认证都合并到
// extension 段，且认证从 Bearer 换成 Ed25519 签名，所以必须同时给出
// 一把私钥——否则上游实现会在构造客户端时就失败。
func loadAvatarConfigWithExtension(t *testing.T, upstreamURL string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	_ = pub
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "error", "format": "json"},
		"extension": {"enabled": true, "base_url": "` + upstreamURL + `/api/v1", "private_key_pem": ` + strconv.Quote(pemText) + `, "timeout_seconds": 5,
		              "features": {"avatar": true}},
		"gravatar": {"enabled": true, "default_size": 256, "default_style": "identicon", "rating": "g"}
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
}

// avatarUserColumns / avatarCacheColumns 与模型字段保持一致。
func avatarUserColumns() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at", "username", "password_hash", "email", "role", "is_active"}
}

func avatarCacheColumns() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "variant", "email_hash", "image_data", "content_type", "etag", "last_modified"}
}

// expectAvatarUser 安排一次用户查询。
func expectAvatarUser(mock sqlmock.Sqlmock, email string) {
	mock.ExpectQuery(`SELECT \* FROM "users"`).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(avatarUserColumns()).
			AddRow(1, nil, nil, nil, "u", "hash", email, "user", true))
}

// expectAvatarCacheEmpty 安排一次空缓存查询（未命中）。
func expectAvatarCacheEmpty(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT \* FROM "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows(avatarCacheColumns()))
}

// expectAvatarCacheHit 安排一次命中的缓存查询。
func expectAvatarCacheHit(mock sqlmock.Sqlmock, variant, emailHash string, data []byte) {
	mock.ExpectQuery(`SELECT \* FROM "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows(avatarCacheColumns()).
			AddRow(1, nil, nil, nil, 1, variant, emailHash, data, "image/png", `"upstream"`, "lm"))
}

// expectAvatarInsert 安排写入缓存的事务。
func expectAvatarInsert(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "avatar_caches"`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`INSERT INTO "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()
}

// extensionVariant 复算 extension 源下的缓存变体。
//
// 这里刻意重复一次实现：测试要断言「缓存行的变体必须与当前配置一致」，
// 若直接调用被测代码就失去意义了。
func extensionVariant(endpoint string, size int) string {
	sum := sha256.Sum256([]byte(endpoint))
	return "ext:" + hex.EncodeToString(sum[:6]) + "|" + strconv.Itoa(size) + "|identicon|g"
}

// TestAvatarGetSetsValidatingCacheHeaders 确认头像响应带 ETag 且要求每次校验。
//
// 旧实现给的是 30 天 public 缓存，管理员刷新缓存后浏览器仍显示旧图。
func TestAvatarGetSetsValidatingCacheHeaders(t *testing.T) {
	img := avatarTestPNG(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"upstream-v1"`)
		_, _ = w.Write(img)
	}))
	defer upstream.Close()

	loadAvatarConfigWithExtension(t, upstream.URL)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	expectAvatarUser(mock, "a@b.com")
	expectAvatarCacheEmpty(mock) // 快路径
	expectAvatarCacheEmpty(mock) // singleflight 内复查
	expectAvatarInsert(mock)

	r := gin.New()
	r.GET("/api/v1/avatar/:id", AvatarGet)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/avatar/1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != avatarCacheControl {
		t.Errorf("Cache-Control = %q，期望 %q", got, avatarCacheControl)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("缺少 ETag，条件请求无法生效")
	}
	if !bytes.Equal(w.Body.Bytes(), img) {
		t.Error("响应体与上游图片不一致")
	}
}

// TestAvatarGetReturnsNotModified 确认 If-None-Match 命中时返回 304 且无 body。
func TestAvatarGetReturnsNotModified(t *testing.T) {
	img := avatarTestPNG(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(img)
	}))
	defer upstream.Close()

	loadAvatarConfigWithExtension(t, upstream.URL)

	u := model.User{Email: "a@b.com"}
	variant := extensionVariant(upstream.URL+"/api/v1", 256)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	r := gin.New()
	r.GET("/api/v1/avatar/:id", AvatarGet)

	// 第一次：下载并返回 ETag。
	expectAvatarUser(mock, "a@b.com")
	expectAvatarCacheEmpty(mock)
	expectAvatarCacheEmpty(mock)
	expectAvatarInsert(mock)

	first := httptest.NewRecorder()
	r.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/avatar/1", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("首次状态码 = %d，body=%s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("首次响应缺少 ETag")
	}

	// 第二次：命中缓存 + If-None-Match → 304，且不重复下载。
	expectAvatarUser(mock, "a@b.com")
	expectAvatarCacheHit(mock, variant, u.GravatarHash(), img)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatar/1", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	r.ServeHTTP(second, req)

	if second.Code != http.StatusNotModified {
		t.Fatalf("二次状态码 = %d，期望 304；body=%s", second.Code, second.Body.String())
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 不应带 body，实际 %d 字节", second.Body.Len())
	}
	if got := second.Header().Get("Cache-Control"); got != avatarCacheControl {
		t.Errorf("304 的 Cache-Control = %q，期望 %q", got, avatarCacheControl)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestEtagMatches 覆盖条件请求头的常见写法。
func TestEtagMatches(t *testing.T) {
	const etag = `"abc"`

	cases := []struct {
		name   string
		header string
		etag   string
		want   bool
	}{
		{"完全一致", `"abc"`, etag, true},
		{"弱校验前缀", `W/"abc"`, etag, true},
		{"逗号分隔", `"other", "abc"`, etag, true},
		{"通配", `*`, etag, true},
		{"不匹配", `"other"`, etag, false},
		{"空请求头", ``, etag, false},
		{"空 ETag", `"abc"`, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := etagMatches(tc.header, tc.etag); got != tc.want {
				t.Errorf("etagMatches(%q, %q) = %v，期望 %v", tc.header, tc.etag, got, tc.want)
			}
		})
	}
}

// TestAvatarGetMeDisablesCaching 确认 /avatar/me 禁止任何缓存。
//
// 该 URL 不区分用户，public 或 private 缓存都会在切换账号时串号。
func TestAvatarGetMeDisablesCaching(t *testing.T) {
	img := avatarTestPNG(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(img)
	}))
	defer upstream.Close()

	loadAvatarConfigWithExtension(t, upstream.URL)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	expectAvatarUser(mock, "a@b.com")
	expectAvatarCacheEmpty(mock)
	expectAvatarCacheEmpty(mock)
	expectAvatarInsert(mock)

	r := gin.New()
	r.GET("/api/v1/avatar/me", func(c *gin.Context) {
		c.Set("user", &model.User{BaseModel: model.BaseModel{ID: 1}, Username: "u", Email: "a@b.com"})
		AvatarGetMe(c)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/avatar/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != avatarNoStore {
		t.Errorf("Cache-Control = %q，期望 %q", got, avatarNoStore)
	}
}

// TestAvatarGetMapsUpstreamFailureTo502 确认上游故障与“没有头像”可区分。
func TestAvatarGetMapsUpstreamFailureTo502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	loadAvatarConfigWithExtension(t, upstream.URL)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	expectAvatarUser(mock, "a@b.com")
	expectAvatarCacheEmpty(mock)
	expectAvatarCacheEmpty(mock)

	r := gin.New()
	r.GET("/api/v1/avatar/:id", AvatarGet)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/avatar/1", nil))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", w.Code)
	}
}

// TestAvatarGetDisabledReturns404 确认关闭开关时前端可降级为占位。
func TestAvatarGetDisabledReturns404(t *testing.T) {
	loadAvatarConfig(t, `, "enabled": false`)

	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	r := gin.New()
	r.GET("/api/v1/avatar/:id", AvatarGet)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/avatar/1", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", w.Code)
	}
}

// TestAvatarGetInvalidID 确认非数字 ID 返回 400。
func TestAvatarGetInvalidID(t *testing.T) {
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	r := gin.New()
	r.GET("/api/v1/avatar/:id", AvatarGet)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/avatar/abc", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", w.Code)
	}
}

// TestAvatarRefreshRejectsUnknownUser 确认刷新接口对不存在的用户返回 404。
func TestAvatarRefreshRejectsUnknownUser(t *testing.T) {
	loadAvatarConfig(t, ``)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc = &service.Services{Gravatar: service.NewGravatarService(db)}

	mock.ExpectQuery(`SELECT \* FROM "users"`).
		WithArgs(uint64(9), 1).
		WillReturnRows(sqlmock.NewRows(avatarUserColumns()))

	r := gin.New()
	r.POST("/api/v1/admin/users/:id/refresh-avatar", AvatarRefresh)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/9/refresh-avatar", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404；body=%s", w.Code, w.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if resp.Code != CodeUserNotFound {
		t.Errorf("业务码 = %d，期望 %d", resp.Code, CodeUserNotFound)
	}
}
