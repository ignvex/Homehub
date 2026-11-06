package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/handler"
)

// TestShutdownEndsOpenSSEStream 防的是「一条打开的日志流拖住整个关闭」。
//
// http.Server.Shutdown 只等空闲连接，SSE 连接永远不空闲。没有在 Shutdown
// 开始时通知流结束，每次 docker stop 都要等满 shutdownTimeout 才强制断开。
// 这里给 serve 一个很长的超时：只有流被主动结束，才能在短时间内返回。
func TestShutdownEndsOpenSSEStream(t *testing.T) {
	// 跑两轮：断开流的开关若是进程级一次性的，第二轮的流会一打开就结束，
	// 或者第一轮之后再也断不开。
	for round := 1; round <= 2; round++ {
		t.Run(fmt.Sprintf("round%d", round), testShutdownEndsOpenSSEStream)
	}
}

func testShutdownEndsOpenSSEStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stream", handler.LogStream)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newHTTPServer(ln.Addr().String(), r)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, srv, ln, 30*time.Second) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/stream")
	if err != nil {
		t.Fatalf("打开 SSE 流失败: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	// 流必须在关闭前一直保持打开。
	firstByte := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = resp.Body.Read(buf)
		close(firstByte)
	}()
	select {
	case <-firstByte:
		t.Fatal("关闭前 SSE 流不应结束")
	case <-time.After(200 * time.Millisecond):
	}

	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("有打开的 SSE 流时关闭没有及时完成")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("关闭耗时 %v，SSE 流没有被主动结束", elapsed)
	}

	// 客户端侧应读到 EOF，而不是一直挂着。
	select {
	case <-firstByte:
	case <-time.After(2 * time.Second):
		t.Fatal("关闭后客户端仍挂在流上")
	}
}

// TestRunFailsFastWhenPortTaken 防的是端口冲突时进程「假活着」。
//
// 绑定放在 goroutine 里时，失败只记一条日志，Run 继续阻塞等信号：
// 容器显示 running，健康检查一直失败，重启策略也不会介入。
func TestRunFailsFastWhenPortTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	body := fmt.Sprintf(`{
		"server": {"host": "127.0.0.1", "port": %d},
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "%s"},
		"log": {"level": "info", "format": "json"}
	}`, port, strings.Repeat("a", 32))
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.Load(cfgPath); err != nil {
		t.Fatalf("加载临时配置失败: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- Run(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("端口被占用时 Run 应返回错误")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("端口被占用时 Run 没有返回")
	}
}
