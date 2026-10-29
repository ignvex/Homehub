package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyURLFor 返回该代理对指定请求 scheme 应使用的地址。
//
// https 请求优先用 HTTPSURL；它为空时回落到 HTTPURL —— 绝大多数 HTTP
// 代理本身支持 CONNECT，同一地址可以同时服务两种协议，因此为空回落
// 比直接不代理更符合预期。
//
// SOCKS5 代理没有 scheme 之分，一律使用 HTTPURL 字段（该字段实际存的是
// host:port，见 buildTransport）。
func (p ProxyEntry) ProxyURLFor(scheme string) string {
	if scheme == "https" && p.HTTPSURL != "" {
		return p.HTTPSURL
	}
	return p.HTTPURL
}

// buildTransport 把一条代理配置转换成可用的 *http.Transport。
//
// 这是"配置 → 网络行为"的唯一实现，favicon / gravatar / RSS 三个模块共用。
// 之前三处各自内联同样的逻辑，导致 HTTPSURL 被全部忽略（配置了不生效）。
func buildTransport(p ProxyEntry) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     90 * time.Second,
	}

	switch p.Type {
	case "socks5":
		// SOCKS5 只能通过自定义拨号器实现，且不支持按 scheme 分流。
		//
		// 注意：proxy.SOCKS5 不校验地址，传错也会成功返回，
		// 失败要拖到实际拨号时。这里提前校验，让配置错误尽早暴露。
		host, port, err := net.SplitHostPort(p.HTTPURL)
		if err != nil {
			return nil, fmt.Errorf("socks5 地址应为 host:port（不带 scheme）: %q", p.HTTPURL)
		}
		if host == "" {
			return nil, fmt.Errorf("socks5 地址缺少主机名: %q", p.HTTPURL)
		}
		if n, convErr := strconv.Atoi(port); convErr != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("socks5 端口无效: %q", port)
		}

		dialer, err := proxy.SOCKS5("tcp", p.HTTPURL, nil, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("创建 SOCKS5 拨号器失败: %w", err)
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}

	default: // "http" 或空
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			addr := p.ProxyURLFor(req.URL.Scheme)
			if addr == "" {
				return nil, nil
			}
			return url.Parse(addr)
		}
	}

	return transport, nil
}

// TransportFor 按给定的代理名列表构建 *http.Transport。
//
// 取列表中第一条可用代理（保持配置顺序即优先级）。names 为空、
// 或没有一条启用时，返回直连用的 transport。
//
// 返回的错误只在代理配置本身非法时出现（如 SOCKS5 地址写法错误），
// 调用方可选择记录日志并继续直连。
func TransportFor(names []string) (*http.Transport, error) {
	direct := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     90 * time.Second,
	}

	for _, p := range ResolveProxy(names) {
		// 跳过地址为空的条目，避免配置写着但没填地址时静默失效
		if p.HTTPURL == "" && p.HTTPSURL == "" {
			continue
		}
		return buildTransport(p)
	}

	return direct, nil
}
