package database

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ignvex/HomeHub/internal/config"
)

// resetForTest 重置全局状态，允许在测试中多次调用 Init。
//
// 仅用于测试，不得在生产代码中调用。
func resetForTest() {
	gDBOnce = sync.Once{}
	gDB = nil
}

func TestBuildDSN(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Host:     "10.0.0.1",
		Port:     5432,
		User:     "app",
		Password: "secret",
		DBName:   "appdb",
		SSLMode:  "require",
	}

	dsn := buildDSN(cfg)

	want := "host=10.0.0.1 port=5432 user=app password=secret dbname=appdb sslmode=require"
	if dsn != want {
		t.Errorf("DSN = %q, want %q", dsn, want)
	}

	// 确保包含所有关键字段
	for _, key := range []string{"host=", "port=", "user=", "password=", "dbname=", "sslmode="} {
		if !strings.Contains(dsn, key) {
			t.Errorf("DSN 缺少 %q 字段: %s", key, dsn)
		}
	}
}

func TestBuildDSN_Defaults(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Host:    "localhost",
		User:    "homehub",
		DBName:  "homehub",
		SSLMode: "disable",
	}

	dsn := buildDSN(cfg)

	if !strings.Contains(dsn, "port=0") {
		t.Error("端口默认值应输出 port=0")
	}
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Error("应包含 sslmode=disable")
	}
}

func TestInit_NilConfig(t *testing.T) {
	resetForTest()

	_, err := Init(nil)
	if err == nil {
		t.Fatal("预期错误，但 err 为 nil")
	}
	if !strings.Contains(err.Error(), "不能为 nil") {
		t.Errorf("错误信息不包含预期内容: %v", err)
	}
}

func TestDB_Uninitialized(t *testing.T) {
	resetForTest()

	db := DB()
	if db != nil {
		t.Error("未初始化时 DB() 应返回 nil")
	}
}

func TestPing_Uninitialized(t *testing.T) {
	resetForTest()

	err := Ping()
	if err == nil {
		t.Fatal("预期错误，但 err 为 nil")
	}
	if !errors.Is(err, err) {
		// 仅确保返回了非 nil 错误
	}
	if !strings.Contains(err.Error(), "未初始化") {
		t.Errorf("错误信息不包含预期内容: %v", err)
	}
}

func TestClose_Uninitialized(t *testing.T) {
	resetForTest()

	// Close 在未初始化时应无 panic
	Close()
}

func TestStats_Uninitialized(t *testing.T) {
	resetForTest()

	s := Stats()
	if s != nil {
		t.Error("未初始化时 Stats() 应返回 nil")
	}
}

// TestInit_Integration 集成测试：当 HOMEHUB_TEST_DB 环境变量设置时，
// 使用真实数据库验证 Init 全流程。
func TestInit_Integration(t *testing.T) {
	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	resetForTest()

	// 从 DSN 解析配置（简化：直接硬编码 localhost 配置）
	cfg := &config.DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "homehub",
		Password: "homehub",
		DBName:   "homehub_test",
		SSLMode:  "disable",
	}

	db, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	if db == nil {
		t.Fatal("Init 返回 nil db")
	}

	// 验证 DB() 返回同一实例
	if DB() != db {
		t.Error("DB() 与 Init 返回的实例不一致")
	}

	// 验证 Ping
	if err := Ping(); err != nil {
		t.Errorf("Ping 失败: %v", err)
	}

	// 验证 Stats
	stats := Stats()
	if stats == nil {
		t.Error("Stats 返回 nil")
	}

	// 验证重复调用 Init（sync.Once 保护）
	db2, err := Init(cfg)
	if err != nil {
		t.Fatalf("第二次 Init 失败: %v", err)
	}
	if db2 != db {
		t.Error("两次 Init 应返回同一实例")
	}

	// 清理
	Close()
}
