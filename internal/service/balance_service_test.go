package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// balanceColumns 与 model.BalanceTarget 的列一一对应。
func balanceColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "deleted_at",
		"name", "type", "base_url", "api_key", "enabled", "sort_order",
	}
}

// TestBalanceListNeverReturnsPlaintextKey 是最重要的一条断言。
//
// api_key 一旦出现在响应里，浏览器缓存、开发者工具、日志中间件都可能
// 把它复制走。这里逐字段检查整个 JSON 视图，确认只出现掩码。
func TestBalanceListNeverReturnsPlaintextKey(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	const secret = "sk-live-9f8e7d6c5b4a3210"
	mock.ExpectQuery(`SELECT \* FROM "balance_targets"`).
		WillReturnRows(sqlmock.NewRows(balanceColumns()).
			AddRow(1, time.Now(), time.Now(), nil, "DeepSeek 主号", "deepseek",
				"https://api.deepseek.com", secret, true, 0))

	svc := NewBalanceService(db)
	items, err := svc.ListTargets()
	if err != nil {
		t.Fatalf("列出监控点失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应有 1 条，实际 %d", len(items))
	}

	v := items[0]
	if !v.HasAPIKey {
		t.Fatal("已配置密钥时 has_api_key 应为 true")
	}
	if v.APIKeyMasked == "" {
		t.Fatal("应返回掩码")
	}
	if strings.Contains(v.APIKeyMasked, secret) {
		t.Fatalf("掩码里不能包含完整密钥: %q", v.APIKeyMasked)
	}
	// 掩码保留头尾各 4 位，中间固定 8 个星号。
	if !strings.HasPrefix(v.APIKeyMasked, "sk-l") || !strings.HasSuffix(v.APIKeyMasked, "3210") {
		t.Fatalf("掩码应保留头尾各 4 位，实际 %q", v.APIKeyMasked)
	}
	// 中间段长度固定，不能从掩码长度反推真实密钥长度。
	if !strings.Contains(v.APIKeyMasked, "********") {
		t.Fatalf("中间段应为固定长度掩码，实际 %q", v.APIKeyMasked)
	}
}

func TestMaskAPIKeyHidesShortKeysEntirely(t *testing.T) {
	// 短密钥露 4 位等于露一半，干脆全遮。
	for _, key := range []string{"", "abc", "12345678"} {
		got := model.MaskAPIKey(key)
		if key == "" {
			if got != "" {
				t.Fatalf("空密钥应返回空串，实际 %q", got)
			}
			continue
		}
		if strings.Contains(got, key) {
			t.Fatalf("短密钥 %q 不应被部分暴露，实际 %q", key, got)
		}
	}
}

func TestBalanceUpdateKeepsKeyWhenOmitted(t *testing.T) {
	// api_key 省略 = 不改。若这里误当成空串处理，用户改个名字就把
	// 密钥清空了，而下一次查询才开始报错——现场已经离开很久。
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "balance_targets"`).
		WithArgs(uint64(3), 1).
		WillReturnRows(sqlmock.NewRows(balanceColumns()).
			AddRow(3, time.Now(), time.Now(), nil, "旧名字", "deepseek",
				"https://api.deepseek.com", "sk-keep-me-12345678", true, 0))

	mock.ExpectQuery(`SELECT count\(\*\) FROM "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE "balance_targets"`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectQuery(`SELECT \* FROM "balance_targets"`).
		WithArgs(uint64(3), 1).
		WillReturnRows(sqlmock.NewRows(balanceColumns()).
			AddRow(3, time.Now(), time.Now(), nil, "新名字", "deepseek",
				"https://api.deepseek.com", "sk-keep-me-12345678", true, 0))

	name := "新名字"
	svc := NewBalanceService(db)
	view, err := svc.UpdateTarget(3, BalanceTargetInput{Name: &name})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if view.Name != "新名字" {
		t.Fatalf("名称应已更新，实际 %q", view.Name)
	}
	if !view.HasAPIKey {
		t.Fatal("省略 api_key 时必须保留原密钥")
	}
}

func TestBalanceUpdateClearsKeyOnExplicitEmptyString(t *testing.T) {
	// 显式空串才是「清空密钥」。这是三态语义的另一半。
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "balance_targets"`).
		WithArgs(uint64(4), 1).
		WillReturnRows(sqlmock.NewRows(balanceColumns()).
			AddRow(4, time.Now(), time.Now(), nil, "x", "deepseek",
				"https://api.deepseek.com", "sk-old-1234567890", true, 0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE "balance_targets"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT \* FROM "balance_targets"`).
		WithArgs(uint64(4), 1).
		WillReturnRows(sqlmock.NewRows(balanceColumns()).
			AddRow(4, time.Now(), time.Now(), nil, "x", "deepseek",
				"https://api.deepseek.com", "", true, 0))

	empty := ""
	svc := NewBalanceService(db)
	view, err := svc.UpdateTarget(4, BalanceTargetInput{APIKey: &empty})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if view.HasAPIKey {
		t.Fatal("显式空串应清空密钥")
	}
	if view.APIKeyMasked != "" {
		t.Fatalf("清空后不应返回掩码，实际 %q", view.APIKeyMasked)
	}
}

func TestBalanceCreateRequiresAPIKey(t *testing.T) {
	// 没有密钥的监控点查询必然失败。让它建出来只是给用户埋一个红点，
	// 而错误要等到第一次刷新才出现。
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	name := "无密钥"
	svc := NewBalanceService(db)
	_, err := svc.CreateTarget(BalanceTargetInput{Name: &name})

	if err == nil {
		t.Fatal("缺少 api_key 时必须拒绝创建")
	}
	if !errors.Is(err, ErrBalanceMissingField) {
		t.Fatalf("错误应可识别为缺字段，实际 %v", err)
	}
}

// ptrString 返回字符串指针，省掉一堆中间变量。
func ptrString(s string) *string { return &s }

func TestBalanceCreateRejectsUnknownType(t *testing.T) {
	// 类型决定查询分支。认不出的类型不会被拒绝，而是在刷新时
	// 每次都报「不支持的类型」——在写入时就挡掉，错误才指向正确的人。
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	name, kind, key := "x", "openai", "sk-abcdefghijk"
	svc := NewBalanceService(db)
	_, err := svc.CreateTarget(BalanceTargetInput{Name: &name, Type: &kind, BaseURL: ptrString("https://api.openai.com"), APIKey: &key})

	if err == nil {
		t.Fatal("不支持的供应商类型必须拒绝")
	}
	if !errors.Is(err, ErrBalanceInvalidType) {
		t.Fatalf("错误应可识别为类型非法，实际 %v", err)
	}
}

func TestBalanceCreateDefaultsBaseURLForDeepSeek(t *testing.T) {
	// 新建时省掉查文档：类型定了，地址就有默认值。
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// 先查重名，再插入。
	mock.ExpectQuery(`SELECT count\(\*\) FROM "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))

	name, key := "主号", "sk-abcdefghijkl"
	svc := NewBalanceService(db)
	view, err := svc.CreateTarget(BalanceTargetInput{Name: &name, APIKey: &key})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if view.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("deepseek 应补默认地址，实际 %q", view.BaseURL)
	}
	if view.Type != "deepseek" {
		t.Fatalf("未指定类型时应默认 deepseek，实际 %q", view.Type)
	}
	if !view.Enabled {
		t.Fatal("新建默认启用")
	}
}

func TestBalanceMigrateImportsOnlyWhenTableEmpty(t *testing.T) {
	// 表非空说明用户已经在 Web 端管理过，再导入会造出重复条目。
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// config 里有两个监控点。
	cfgPath := writeBalanceConfig(t)
	if err := config.Load(cfgPath); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	mock.ExpectQuery(`SELECT count\(\*\) FROM "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	svc := NewBalanceService(db)
	if err := svc.MigrateTargetsFromConfig(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	// 只允许那一次 count 查询，没有任何 INSERT。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("表非空时不应写入任何行: %v", err)
	}
}

func TestBalanceMigratePreservesOrderAndDefaults(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	cfgPath := writeBalanceConfig(t)
	if err := config.Load(cfgPath); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	mock.ExpectQuery(`SELECT count\(\*\) FROM "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO "balance_targets"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))

	svc := NewBalanceService(db)
	if err := svc.MigrateTargetsFromConfig(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// writeBalanceConfig 写一份带两个监控点的最小配置。
//
// 第二个故意把 type 与 base_url 留空，用来验证迁移时的兜底。
func writeBalanceConfig(t *testing.T) string {
	t.Helper()

	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "error", "format": "json"},
		"balance_monitor": {
			"enabled": true,
			"refresh_interval_seconds": 60,
			"targets": [
				{"name": "主号", "type": "deepseek", "base_url": "https://api.deepseek.com", "api_key": "sk-a", "enabled": true},
				{"name": "备用", "type": "deepseek", "base_url": "https://api.deepseek.com/", "api_key": "sk-b", "enabled": false}
			]
		}
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	return path
}
