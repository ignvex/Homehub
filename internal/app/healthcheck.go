package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
)

// healthcheckTimeout 是单次探测的上限。
//
// 编排系统按固定间隔调用探测命令，探测自己卡住就等于没有探测；
// 超时给得比 server 处理任何请求都宽裕，又短到不拖住调用方。
const healthcheckTimeout = 3 * time.Second

// Healthcheck 探测本机运行中的 HomeHub，健康时返回 nil。
//
// 供 `homehub -healthcheck` 使用，退出码即结果。只读取配置里的
// server.host / server.port，不走 config.Load：那条路径会创建默认
// 配置、收紧文件权限、要求数据库字段齐全，一次探测不该有这些副作用。
//
// 判断标准与 /api/v1/health 一致：能返回 200 即视为存活。数据库不可达时
// 该接口仍返回 200，这里也照此处理——探测回答的是「进程还在服务吗」。
func Healthcheck(configPath string) error {
	host, port, err := probeTarget(resolveConfigPath(configPath))
	if err != nil {
		return err
	}

	url := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/api/v1/health"
	client := &http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("探测 %s 失败: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("探测 %s 返回 %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// probeTarget 从配置文件里取监听地址，返回可以直接拨号的主机和端口。
func probeTarget(path string) (string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	// 只解析 server 段：配置文件里其余部分对探测没有意义，
	// 字段缺失时按监听默认值处理，与 Load 的默认值保持一致。
	var raw struct {
		Server config.ServerConfig `json:"server"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", 0, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}

	host := raw.Server.Host
	if host == "" {
		host = config.DefaultServerHost
	}
	port := raw.Server.Port
	if port == 0 {
		port = config.DefaultServerPort
	}

	return probeHost(host), port, nil
}

// probeHost 把监听地址换成可拨号的地址。
//
// 0.0.0.0 与 :: 是通配地址，连接它们并不会落到本机，必须换成回环地址。
// 监听在具体地址或主机名上时原样使用。
func probeHost(host string) string {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !addr.IsUnspecified() {
		return host
	}
	if addr.Is4() {
		return "127.0.0.1"
	}
	return "::1"
}
