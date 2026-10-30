package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// userColumns 是查询 user 表所需的列。
func userColumns() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at", "username", "password_hash", "email", "role", "is_active"}
}

// cacheColumns 是查询 avatar_caches 表所需的列（含 Variant）。
func cacheColumns() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "variant", "email_hash", "image_data", "content_type", "etag", "last_modified"}
}

// testPNG 返回一张合法的 1x1 PNG，避免直接拼魔数骗过嗅探。
func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("生成 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// currentVariant 返回当前配置与来源下应有的缓存变体。
func currentVariant(source avatarSource) string {
	cfg := config.Get()
	return avatarVariant(source, resolveAvatarSize(cfg),
		cfg.Gravatar.DefaultStyle, cfg.Gravatar.Rating)
}

// expectUser 安排一次用户查询。
func expectUser(mock sqlmock.Sqlmock, id uint64, email string) {
	mock.ExpectQuery(`SELECT \* FROM "users"`).
		WithArgs(id, 1).
		WillReturnRows(sqlmock.NewRows(userColumns()).
			AddRow(id, nil, nil, nil, "u", "hash", email, "user", true))
}

// expectCacheMiss 安排一次头像缓存查询并返回空结果。
func expectCacheMiss(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT \* FROM "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows(cacheColumns()))
}

// expectCachedAvatar 安排一次命中缓存的查询。
func expectCachedAvatar(mock sqlmock.Sqlmock, variant, emailHash string, data []byte, contentType string) {
	mock.ExpectQuery(`SELECT \* FROM "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows(cacheColumns()).
			AddRow(1, nil, nil, nil, 1, variant, emailHash, data, contentType, "etag-upstream", "lm"))
}

// expectAvatarWrite 安排事务内的「删旧 + 插新」。
func expectAvatarWrite(mock sqlmock.Sqlmock, rowsChanged int64, newID int64) {
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "avatar_caches"`).
		WillReturnResult(sqlmock.NewResult(0, rowsChanged))
	mock.ExpectQuery(`INSERT INTO "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectCommit()
}

// fakeAvatarSource 是可控的上游实现，用于隔离网络与验证去重。
type fakeAvatarSource struct {
	key         string
	payload     *avatarPayload
	err         error
	notModified bool
	calls       int32

	started chan struct{} // 首次进入 Fetch 时通知（可选）
	release chan struct{} // 关闭后 Fetch 才返回（可选）
}

func (f *fakeAvatarSource) Key() string { return f.key }

func (f *fakeAvatarSource) Fetch(context.Context, avatarRequest, *avatarConditions) (*avatarPayload, bool, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		<-f.release
	}
	return f.payload, f.notModified, f.err
}

// TestGetAvatarDataUsesCacheForMatchingVariant 确认邮箱与变体都匹配时命中缓存。
func TestGetAvatarDataUsesCacheForMatchingVariant(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	u := model.User{Email: "a@b.com"}
	u.ID = 1
	variant := currentVariant(newAvatarSource(config.Get()))

	expectUser(mock, 1, "a@b.com")
	expectCachedAvatar(mock, variant, u.GravatarHash(), []byte("img"), "image/png")

	avatar, err := svc.GetAvatarData(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAvatarData: %v", err)
	}
	if string(avatar.Data) != "img" || avatar.ContentType != "image/png" {
		t.Errorf("未命中缓存，返回 %q / %q", avatar.Data, avatar.ContentType)
	}
	if avatar.ETag == "" {
		t.Error("命中缓存时也应返回 ETag，否则条件请求无法生效")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期（不应有下载）: %v", err)
	}
}

// TestGetAvatarDataInvalidatesOnEmailChange 确认改邮箱后不再使用旧缓存。
func TestGetAvatarDataInvalidatesOnEmailChange(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	// 图片在启动服务器前生成，避免在 handler goroutine 里调用 t.Fatalf。
	img := testPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"upstream"`)
		_, _ = w.Write(img)
	}))
	defer srv.Close()
	svc.sourceFactory = func(*config.Config) avatarSource {
		return &directAvatarSource{client: srv.Client(), baseURL: srv.URL}
	}

	oldUser := model.User{Email: "old@b.com"}
	oldHash := oldUser.GravatarHash()
	variant := currentVariant(svc.sourceFor(config.Get()))

	expectUser(mock, 1, "new@b.com")
	// 旧缓存：变体匹配但邮箱哈希对不上，Go 侧必须判定为未命中。
	expectCachedAvatar(mock, variant, oldHash, []byte("stale"), "image/png")
	// singleflight 内复查：数据库中确实没有匹配行。
	expectCacheMiss(mock)
	expectAvatarWrite(mock, 1, 2)

	avatar, err := svc.GetAvatarData(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAvatarData: %v", err)
	}
	if string(avatar.Data) == "stale" {
		t.Error("邮箱变更后仍返回旧缓存")
	}
	if avatar.ContentType != "image/png" {
		t.Errorf("Content-Type = %q", avatar.ContentType)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestGetAvatarDataInvalidatesOnConfigChange 确认改了尺寸/风格后不会继续用旧图。
//
// 这是重构前最容易被忽略的一条：缓存 key 只看邮箱，改配置后头像永远是旧的。
func TestGetAvatarDataInvalidatesOnConfigChange(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 256, "default_style": "robohash", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	img := testPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(img)
	}))
	defer srv.Close()
	svc.sourceFactory = func(*config.Config) avatarSource {
		return &directAvatarSource{client: srv.Client(), baseURL: srv.URL}
	}

	u := model.User{Email: "a@b.com"}
	u.ID = 1

	expectUser(mock, 1, "a@b.com")
	// 组网时用的是 identicon，现在配置已改为 robohash。
	expectCachedAvatar(mock, "direct:"+srv.URL+"|200|identicon|g", u.GravatarHash(), []byte("stale"), "image/png")
	expectCacheMiss(mock)
	expectAvatarWrite(mock, 1, 2)

	avatar, err := svc.GetAvatarData(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAvatarData: %v", err)
	}
	if string(avatar.Data) == "stale" {
		t.Error("配置变更后仍返回旧缓存")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestGetAvatarDataRejectsNonImage 确认上游返回 HTML 时不写缓存。
func TestGetAvatarDataRejectsNonImage(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 伪装成图片的 HTML：只看 Content-Type 的实现会把它存进缓存。
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
	}))
	defer srv.Close()
	svc.sourceFactory = func(*config.Config) avatarSource {
		return &directAvatarSource{client: srv.Client(), baseURL: srv.URL}
	}

	expectUser(mock, 1, "a@b.com")
	expectCacheMiss(mock) // 快路径
	expectCacheMiss(mock) // singleflight 内复查

	_, err := svc.GetAvatarData(context.Background(), 1)
	if !errors.Is(err, ErrAvatarInvalid) {
		t.Fatalf("错误 = %v，期望 ErrAvatarInvalid", err)
	}
	// 没有 Begin/Create 期望：任何写入都会让 ExpectationsWereMet 失败。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("不合法响应不应写缓存: %v", err)
	}
}

// TestGetAvatarDataNegativeCache 确认上游 404 后短时间内不再重复请求。
func TestGetAvatarDataNegativeCache(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_style": "404"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	// fakeAvatarSource 自带 calls 计数，用来确认负缓存生效。
	src := &fakeAvatarSource{key: "fake", err: ErrAvatarNotFound}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	expectUser(mock, 1, "a@b.com")
	expectCacheMiss(mock) // 快路径
	expectCacheMiss(mock) // singleflight 内复查
	if _, err := svc.GetAvatarData(context.Background(), 1); !errors.Is(err, ErrAvatarNotFound) {
		t.Fatalf("首次错误 = %v，期望 ErrAvatarNotFound", err)
	}

	// 第二次：仍然查用户与缓存，但不应再打上游。
	expectUser(mock, 1, "a@b.com")
	expectCacheMiss(mock)
	if _, err := svc.GetAvatarData(context.Background(), 1); !errors.Is(err, ErrAvatarNotFound) {
		t.Fatalf("二次错误 = %v，期望 ErrAvatarNotFound", err)
	}

	if got := atomic.LoadInt32(&src.calls); got != 1 {
		t.Errorf("上游被调用 %d 次，负缓存应只允许 1 次", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestGetAvatarDataUnavailableWithoutEmail 确认无邮箱时不查缓存、不打上游。
func TestGetAvatarDataUnavailableWithoutEmail(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	expectUser(mock, 1, "")

	if _, err := svc.GetAvatarData(context.Background(), 1); !errors.Is(err, ErrAvatarUnavailable) {
		t.Fatalf("错误 = %v，期望 ErrAvatarUnavailable", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestGetAvatarDataUserNotFound 确认不存在的用户返回分类错误而不是 500。
func TestGetAvatarDataUserNotFound(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	mock.ExpectQuery(`SELECT \* FROM "users"`).
		WithArgs(uint64(99), 1).
		WillReturnRows(sqlmock.NewRows(userColumns()))

	if _, err := svc.GetAvatarData(context.Background(), 99); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("错误 = %v，期望 ErrUserNotFound", err)
	}
}

// TestGetAvatarDataCoalescesConcurrentFetches 确认同一用户并发只下载一次。
//
// 并发请求的 SQL 会交错执行，因此这里用无序匹配，并通过 done channel
// 加超时：匹配意外时测试应当失败，而不是把整个测试套件挂死。
func TestGetAvatarDataCoalescesConcurrentFetches(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewUnorderedMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	src := &fakeAvatarSource{
		key:     "fake",
		payload: &avatarPayload{Data: testPNG(t), ContentType: "image/png"},
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	// 两个请求各查一次用户与缓存；先到者还会在 singleflight 内复查一次缓存。
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(`SELECT \* FROM "users"`).
			WillReturnRows(sqlmock.NewRows(userColumns()).
				AddRow(1, nil, nil, nil, "u", "hash", "a@b.com", "user", true))
	}
	for i := 0; i < 3; i++ {
		mock.ExpectQuery(`SELECT \* FROM "avatar_caches"`).
			WillReturnRows(sqlmock.NewRows(cacheColumns()))
	}
	expectAvatarWrite(mock, 0, 2)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	start := func(idx int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[idx] = svc.GetAvatarData(context.Background(), 1)
		}()
	}

	start(0)
	// 第一个请求已进入上游；它的 user/cache 查询此时已完成。
	select {
	case <-src.started:
	case <-time.After(5 * time.Second):
		t.Fatal("第一个请求未能进入上游下载")
	}

	start(1)
	// 给第二个请求时间完成查询并进入 singleflight 等待。
	time.Sleep(150 * time.Millisecond)

	close(src.release)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("并发请求未在 10s 内完成（singleflight 或 SQL 期望死锁）")
	}

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个请求失败: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&src.calls); got != 1 {
		t.Errorf("上游被调用 %d 次，期望 1 次（singleflight 未生效）", got)
	}
}

// TestRefreshAvatarNotModified 确认上游 304 时不重复下载。
func TestRefreshAvatarNotModified(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	u := model.User{Email: "a@b.com"}
	u.ID = 1
	src := &fakeAvatarSource{key: "fake", notModified: true}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	expectUser(mock, 1, "a@b.com")
	expectCachedAvatar(mock, currentVariant(src), u.GravatarHash(), []byte("img"), "image/png")

	updated, err := svc.RefreshAvatar(context.Background(), 1)
	if err != nil {
		t.Fatalf("RefreshAvatar: %v", err)
	}
	if updated {
		t.Error("304 时不应报告 updated")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期（不应重新写入）: %v", err)
	}
}

// TestRefreshAvatarRedownloadsStaleVariant 确认来源变化后刷新会重新下载而不是复用。
func TestRefreshAvatarRedownloadsStaleVariant(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 512, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	src := &fakeAvatarSource{
		key:     "fake",
		payload: &avatarPayload{Data: testPNG(t), ContentType: "image/png"},
	}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	u := model.User{Email: "a@b.com"}
	u.ID = 1

	expectUser(mock, 1, "a@b.com")
	// 变体来自另一个来源，当前配置下永远不会命中。
	expectCachedAvatar(mock, "direct:https://old.example|200|identicon|g", u.GravatarHash(), []byte("img"), "image/png")
	expectAvatarWrite(mock, 1, 2)

	updated, err := svc.RefreshAvatar(context.Background(), 1)
	if err != nil {
		t.Fatalf("RefreshAvatar: %v", err)
	}
	if !updated {
		t.Error("变体失效时应重新下载并报告 updated")
	}
	if got := atomic.LoadInt32(&src.calls); got != 1 {
		t.Errorf("上游被调用 %d 次，期望 1 次", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestRefreshAvatarRemovesCacheWhenUpstreamSaysMissing 确认上游 404 时清掉本地缓存。
func TestRefreshAvatarRemovesCacheWhenUpstreamSaysMissing(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	u := model.User{Email: "a@b.com"}
	u.ID = 1
	src := &fakeAvatarSource{key: "fake", err: ErrAvatarNotFound}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	expectUser(mock, 1, "a@b.com")
	expectCachedAvatar(mock, currentVariant(src), u.GravatarHash(), []byte("img"), "image/png")
	mock.ExpectExec(`DELETE FROM "avatar_caches"`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	updated, err := svc.RefreshAvatar(context.Background(), 1)
	if err != nil {
		t.Fatalf("RefreshAvatar: %v", err)
	}
	if !updated {
		t.Error("清除失效缓存应报告 updated")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestRefreshAvatarDownloadsWhenNoCache 确认从未缓存过的用户刷新时会建立缓存。
func TestRefreshAvatarDownloadsWhenNoCache(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 256, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	src := &fakeAvatarSource{
		key:     "fake",
		payload: &avatarPayload{Data: testPNG(t), ContentType: "image/png"},
	}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	expectUser(mock, 1, "a@b.com")
	expectCacheMiss(mock)
	expectAvatarWrite(mock, 0, 1)

	updated, err := svc.RefreshAvatar(context.Background(), 1)
	if err != nil {
		t.Fatalf("RefreshAvatar: %v", err)
	}
	if !updated {
		t.Error("新建缓存应报告 updated")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestStoreAvatarWrapsInTransaction 确认写缓存使用事务，避免删旧后插入失败丢缓存。
func TestStoreAvatarWrapsInTransaction(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "avatar_caches"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO "avatar_caches"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := svc.storeAvatar(1, "fake|200|identicon|g", "hash", &avatarPayload{
		Data:        []byte("img"),
		ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("storeAvatar: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期: %v", err)
	}
}

// TestRefreshAvatarPropagatesUpstreamError 确认上游故障不会被当成“无更新”。
func TestRefreshAvatarPropagatesUpstreamError(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	u := model.User{Email: "a@b.com"}
	u.ID = 1
	src := &fakeAvatarSource{key: "fake", err: ErrAvatarUpstream}
	svc.sourceFactory = func(*config.Config) avatarSource { return src }

	expectUser(mock, 1, "a@b.com")
	expectCachedAvatar(mock, currentVariant(src), u.GravatarHash(), []byte("img"), "image/png")

	if _, err := svc.RefreshAvatar(context.Background(), 1); !errors.Is(err, ErrAvatarUpstream) {
		t.Fatalf("错误 = %v，期望 ErrAvatarUpstream", err)
	}
}
