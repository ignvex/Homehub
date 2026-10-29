package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// userColumns 是查询 user 表所需的列。
func userColumns() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at", "username", "password_hash", "email", "role", "is_active"}
}

// cacheColumns 是查询 avatar_caches 表所需的列。
func cacheColumns() []string {
	return []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "email_hash", "image_data", "content_type", "etag", "last_modified"}
}

// TestGetAvatarDataUsesCacheWhenEmailUnchanged 确认邮箱未变时命中缓存、不发外部请求。
func TestGetAvatarDataUsesCacheWhenEmailUnchanged(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	u := model.User{Email: "a@b.com"}
	u.ID = 1

	mock.ExpectQuery(`SELECT \* FROM "homehub_users" WHERE "homehub_users"\."id" = \$1`).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(userColumns()).
			AddRow(1, nil, nil, nil, "u", "hash", "a@b.com", "user", true))

	mock.ExpectQuery(`SELECT \* FROM "homehub_avatar_caches" WHERE user_id = \$1`).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(cacheColumns()).
			AddRow(1, nil, nil, nil, 1, u.GravatarHash(), []byte("img"), "image/png", "etag", "lm"))

	data, ctype, err := svc.GetAvatarData(1)
	if err != nil {
		t.Fatalf("GetAvatarData: %v", err)
	}
	if string(data) != "img" || ctype != "image/png" {
		t.Errorf("未命中缓存，返回 %q / %q", data, ctype)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 不符预期（不应有额外请求）: %v", err)
	}
}

// TestGetAvatarDataInvalidatesOnEmailChange 确认改邮箱后不再使用旧缓存。
//
// 历史上 EmailHash 只写不读，导致改邮箱后头像永远是旧的。
// 现在命中缓存前必须比对哈希；不一致时应重新下载。
func TestGetAvatarDataInvalidatesOnEmailChange(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	mock.ExpectQuery(`SELECT \* FROM "homehub_users" WHERE "homehub_users"\."id" = \$1`).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(userColumns()).
			AddRow(1, nil, nil, nil, "u", "hash", "new@b.com", "user", true))

	// 缓存里的哈希对应旧邮箱
	oldHash := model.User{Email: "old@b.com"}
	mock.ExpectQuery(`SELECT \* FROM "homehub_avatar_caches" WHERE user_id = \$1`).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(cacheColumns()).
			AddRow(1, nil, nil, nil, 1, oldHash.GravatarHash(), []byte("stale"), "image/png", "", ""))

	// 下载指向 httptest：单元测试不依赖外网
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fresh"))
	}))
	defer srv.Close()
	svc.SetBaseURL(srv.URL)

	// 保存顺序：先硬删旧记录，再插入新缓存（无事务，SkipDefaultTransaction）
	mock.ExpectExec(`DELETE FROM "homehub_avatar_caches"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO "homehub_avatar_caches"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))

	data, _, err := svc.GetAvatarData(1)
	if err != nil {
		t.Fatalf("GetAvatarData: %v", err)
	}
	if string(data) != "fresh" {
		t.Errorf("邮箱变更后应重新下载，实际返回 %q", data)
	}
}

// TestGetAvatarDataMissingEmail 确认无邮箱时报错而不是查缓存。
func TestGetAvatarDataMissingEmail(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	mock.ExpectQuery(`SELECT \* FROM "homehub_users"`).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(userColumns()).
			AddRow(1, nil, nil, nil, "u", "hash", "", "user", true))

	if _, _, err := svc.GetAvatarData(1); err == nil {
		t.Error("无邮箱应返回错误")
	}
}

// TestGravatarURLFormat 确认 URL 结构与 Gravatar 约定一致。
//
// URL 构造从 model.User 迁到 service：base 可注入后单元测试
// 才能指向 httptest，不需要访问外网。
func TestGravatarURLFormat(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true, "default_size": 200, "default_style": "identicon", "rating": "g"}`)

	svc := NewGravatarService(nil)
	svc.SetBaseURL("https://www.gravatar.com/avatar")

	got := svc.URL("MyEmailAddress@example.com")
	want := "https://www.gravatar.com/avatar/0bc83cb571cd1c50ba6f3e8a78ef1346?s=200&d=identicon&r=g"
	if got != want {
		t.Errorf("URL() = %q\n期望 %q", got, want)
	}
}

// TestGravatarURLEmptyEmail 确认空邮箱不生成 URL。
func TestGravatarURLEmptyEmail(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": true}`)

	svc := NewGravatarService(nil)
	if got := svc.URL(""); got != "" {
		t.Errorf("空邮箱 URL = %q，期望空", got)
	}
}

// TestGravatarURLDisabled 确认关闭时即使有邮箱也不生成 URL。
func TestGravatarURLDisabled(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": false}`)

	svc := NewGravatarService(nil)
	if got := svc.URL("a@b.com"); got != "" {
		t.Errorf("禁用时 URL = %q，期望空", got)
	}
}
