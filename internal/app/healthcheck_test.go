package app

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProbeConfig 写一份只带 server 段的配置，模拟部署时的真实文件。
func writeProbeConfig(t *testing.T, host string, port int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	body := fmt.Sprintf(`{"server":{"host":%q,"port":%d}}`, host, port)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// healthServer 起一个只响应 /api/v1/health 的测试服务，返回端口。
func healthServer(t *testing.T, status int) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"ok","db":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", u.Port())
	if err != nil {
		t.Fatalf("解析测试服务端口失败: %v", err)
	}
	return port
}

// TestHealthcheckSucceedsAgainstRunningServer 确认探测命令能对一份真实配置
// 做出正确的判断，而不是把「配置能读」当成「服务健康」。
func TestHealthcheckSucceedsAgainstRunningServer(t *testing.T) {
	port := healthServer(t, http.StatusOK)
	if err := Healthcheck(writeProbeConfig(t, "127.0.0.1", port)); err != nil {
		t.Fatalf("服务在跑时应成功: %v", err)
	}
}

// TestHealthcheckProbesLoopbackForWildcardHost 防的是配置照默认值写了
// 0.0.0.0 时探测直接连通配地址：那样每次都会失败，健康检查变成永久噪音。
func TestHealthcheckProbesLoopbackForWildcardHost(t *testing.T) {
	port := healthServer(t, http.StatusOK)
	if err := Healthcheck(writeProbeConfig(t, "0.0.0.0", port)); err != nil {
		t.Fatalf("通配地址应改为探测回环: %v", err)
	}
}

// TestHealthcheckFailsOnNon200 确认非 200 不算健康，且错误里带得回响应内容。
func TestHealthcheckFailsOnNon200(t *testing.T) {
	port := healthServer(t, http.StatusServiceUnavailable)
	err := Healthcheck(writeProbeConfig(t, "127.0.0.1", port))
	if err == nil {
		t.Fatal("返回 503 时应失败")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("错误应带上状态码: %v", err)
	}
}

// TestHealthcheckFailsWhenNothingListens 确认端口没人监听时失败，
// 而不是静默通过。
func TestHealthcheckFailsWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	if err := Healthcheck(writeProbeConfig(t, "127.0.0.1", port)); err == nil {
		t.Fatal("端口无人监听时应失败")
	}
}

// TestHealthcheckReportsMissingConfig 确认配置缺失时报错而不是按默认端口
// 瞎猜：路径写错和「服务真的没起来」需要能区分开。
func TestHealthcheckReportsMissingConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "absent.json")
	err := Healthcheck(p)
	if err == nil {
		t.Fatal("配置不存在时应失败")
	}
	if !strings.Contains(err.Error(), p) {
		t.Errorf("错误应指明路径: %v", err)
	}
}

// TestProbeHost 锁住监听地址到拨号地址的换算。
func TestProbeHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.0.0.0", "127.0.0.1"},
		{"::", "::1"},
		{"127.0.0.1", "127.0.0.1"},
		{"192.168.1.10", "192.168.1.10"},
		{"localhost", "localhost"},
	}
	for _, tc := range cases {
		if got := probeHost(tc.in); got != tc.want {
			t.Errorf("probeHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
