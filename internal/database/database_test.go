package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/jackc/pgx/v5/pgconn"
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

	_, err := Init(context.Background(), nil, 0)
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

// TestInit_Integration 集成测试：设置 HOMEHUB_TEST_DB 时用真实数据库验证
// Init 全流程。
//
// DSN 格式与 pgx 相同：
//
//	HOMEHUB_TEST_DB="host=192.168.1.2 port=5432 user=test password=xxx dbname=testdb sslmode=disable" \
//	  go test ./internal/database/ -run Integration -v
func TestInit_Integration(t *testing.T) {
	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	resetForTest()

	cfg := parseTestDSN(t, dsn)

	db, err := Init(context.Background(), cfg, 0)
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
	db2, err := Init(context.Background(), cfg, 0)
	if err != nil {
		t.Fatalf("第二次 Init 失败: %v", err)
	}
	if db2 != db {
		t.Error("两次 Init 应返回同一实例")
	}

	// 清理
	Close()
}

// parseTestDSN 把 "key=value key=value" 形式的 DSN 解析成 DatabaseConfig。
//
// 只支持集成测试需要的字段；缺项按 PostgreSQL 默认补全。
func parseTestDSN(t *testing.T, dsn string) *config.DatabaseConfig {
	t.Helper()

	cfg := &config.DatabaseConfig{
		Host:    "localhost",
		Port:    5432,
		SSLMode: "disable",
	}
	for _, field := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch k {
		case "host":
			cfg.Host = v
		case "port":
			p, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("DSN 中 port 不是数字: %q", v)
			}
			cfg.Port = p
		case "user":
			cfg.User = v
		case "password":
			cfg.Password = v
		case "dbname":
			cfg.DBName = v
		case "sslmode":
			cfg.SSLMode = v
		}
	}
	if cfg.User == "" || cfg.DBName == "" {
		t.Fatalf("DSN 缺少 user 或 dbname: %q", dsn)
	}
	return cfg
}

// TestRetryableConnectError 锁住启动重试的分类：数据库还没起来要等，
// 服务端明确拒绝（密码错、库不存在）不要等。后者若也重试，用户要多等
// 一分钟才看到同一条报错。
func TestRetryableConnectError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"连接被拒", errors.New("dial tcp 172.18.0.2:5432: connect: connection refused"), true},
		{"数据库正在启动", &pgconn.PgError{Code: "57P03"}, true},
		{"认证失败", &pgconn.PgError{Code: "28P01"}, false},
		{"库不存在", fmt.Errorf("wrap: %w", &pgconn.PgError{Code: "3D000"}), false},
		{"DSN 解析失败", &pgconn.ParseConfigError{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableConnectError(tc.err); got != tc.want {
				t.Errorf("retryableConnectError = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInitStopsWaitingOnCancel 防的是启动等待数据库时 docker stop 不生效：
// 收到信号后必须立即放弃重试，而不是等满整个等待窗口再被 SIGKILL。
func TestInitStopsWaitingOnCancel(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	// 127.0.0.1:1 上没有服务，连接立即被拒，属于可重试错误。
	cfg := &config.DatabaseConfig{Host: "127.0.0.1", Port: 1, User: "u", DBName: "d", SSLMode: "disable"}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Init(ctx, cfg, time.Minute)
	if err == nil {
		t.Fatal("数据库不可达时应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("取消导致的失败应包装 ctx.Err()，got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("取消后仍等待了 %v", elapsed)
	}
}

// TestInitNoWaitTriesOnce 确认 maxWait=0 时只尝试一次，不进入重试。
func TestInitNoWaitTriesOnce(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	cfg := &config.DatabaseConfig{Host: "127.0.0.1", Port: 1, User: "u", DBName: "d", SSLMode: "disable"}
	start := time.Now()
	if _, err := Init(context.Background(), cfg, 0); err == nil {
		t.Fatal("数据库不可达时应返回错误")
	}
	if elapsed := time.Since(start); elapsed >= connectRetryInitial {
		t.Errorf("maxWait=0 不应重试，实际耗时 %v", elapsed)
	}
}

// silentListener 接受连接但从不回应，模拟丢包的数据库主机或卡住的握手。
func silentListener(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	})
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

// TestInitCancelInterruptsHangingAttempt 防的是「单次连接卡住时信号无效」。
//
// pgx 默认拨号没有超时，gorm.Open 自带的 Ping 也不接 ctx：对端丢包时
// 一次尝试要等约两分钟，docker stop 在这期间无效，只能等 SIGKILL。
// 连接被拒的用例覆盖不到这条路径，这里用一个只 accept 不回话的监听器。
func TestInitCancelInterruptsHangingAttempt(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	host, port := silentListener(t)
	cfg := &config.DatabaseConfig{Host: host, Port: port, User: "u", DBName: "d", SSLMode: "disable"}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Init(ctx, cfg, time.Minute)
	if err == nil {
		t.Fatal("对端不回应时应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("取消导致的失败应包装 ctx.Err()，got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("取消后仍卡在连接尝试里 %v", elapsed)
	}
}

// TestInitAttemptRespectsMaxWait 确认单次尝试不会越过等待上限。
func TestInitAttemptRespectsMaxWait(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	host, port := silentListener(t)
	cfg := &config.DatabaseConfig{Host: host, Port: port, User: "u", DBName: "d", SSLMode: "disable"}

	start := time.Now()
	if _, err := Init(context.Background(), cfg, 500*time.Millisecond); err == nil {
		t.Fatal("对端不回应时应返回错误")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("maxWait=500ms，实际等待 %v", elapsed)
	}
}
