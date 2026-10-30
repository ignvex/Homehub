package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// loadConfigWith 把一个最小可用配置写入临时文件并加载为全局配置。
//
// 这些测试串行执行（同包内不使用 t.Parallel），因此替换全局配置是安全的；
// 结束时恢复为“未加载”状态，避免影响后续用例。
func loadConfigWith(t *testing.T, extra string) {
	t.Helper()

	tmp := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "info", "format": "json"}` + extra + `
	}`
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	if err := config.Load(tmp); err != nil {
		t.Fatalf("加载临时配置失败: %v", err)
	}
}

// TestPasteDisabledRejectsWrites 确认关闭剪贴板后所有对外操作被拒绝。
//
// 这是一个公开写入接口（无需认证），开关必须真正生效，
// 否则"关掉"只是 UI 上的假象。
func TestPasteDisabledRejectsWrites(t *testing.T) {
	loadConfigWith(t, `,"pastebin": {"enabled": false}`)

	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewPasteService(db)

	if _, _, err := svc.CreatePaste("hello"); !errors.Is(err, ErrPasteDisabled) {
		t.Errorf("CreatePaste 错误 = %v，期望 ErrPasteDisabled", err)
	}
	if _, err := svc.GetPaste("abc"); !errors.Is(err, ErrPasteDisabled) {
		t.Errorf("GetPaste 错误 = %v，期望 ErrPasteDisabled", err)
	}
	if err := svc.DeletePaste("abc", "token"); !errors.Is(err, ErrPasteDisabled) {
		t.Errorf("DeletePaste 错误 = %v，期望 ErrPasteDisabled", err)
	}
	if _, err := svc.ListPastes(); !errors.Is(err, ErrPasteDisabled) {
		t.Errorf("ListPastes 错误 = %v，期望 ErrPasteDisabled", err)
	}
}

// TestPasteEnabledAllowsWrites 确认开启后不再返回禁用错误。
func TestPasteEnabledAllowsWrites(t *testing.T) {
	loadConfigWith(t, `,"pastebin": {"enabled": true}`)

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewPasteService(db)

	// testutil 的 gorm 实例启用了 SkipDefaultTransaction，故无 Begin/Commit。
	mock.ExpectQuery(`INSERT INTO "pastes"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	paste, token, err := svc.CreatePaste("hello")
	if err != nil {
		t.Fatalf("开启后创建失败: %v", err)
	}
	if paste == nil || token == "" {
		t.Error("创建成功但返回值为空")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("未按预期执行 INSERT: %v", err)
	}
}

// TestGravatarDisabledRejectsFetch 确认关闭 Gravatar 后不再对外发请求。
func TestGravatarDisabledRejectsFetch(t *testing.T) {
	loadConfigWith(t, `,"gravatar": {"enabled": false}`)

	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	svc := NewGravatarService(db)

	ctx := context.Background()
	if _, err := svc.GetAvatarData(ctx, 1); !errors.Is(err, ErrGravatarDisabled) {
		t.Errorf("GetAvatarData 错误 = %v，期望 ErrGravatarDisabled", err)
	}
	if _, err := svc.RefreshAvatar(ctx, 1); !errors.Is(err, ErrGravatarDisabled) {
		t.Errorf("RefreshAvatar 错误 = %v，期望 ErrGravatarDisabled", err)
	}
}
