package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/imroc/req/v3"
	"github.com/mmcdole/gofeed"
)

// RSSFetcher 拉取并解析一个订阅源。
//
// 抽出接口的目的：抓取是 HomeHub 里唯一需要访问外部网络、且最容易变慢的
// 环节。把它与入库逻辑解耦后，可以替换成其他实现而无需改动 RSSService：
//
//   - 本地直接抓取（默认，httpFeedFetcher）
//   - 交给外部加速服务（如独立的 extension 进程/容器，集中做并发、缓存与去重）
//
// 实现必须尊重 ctx 的取消与超时；返回的错误应包含足够的上下文用于记录到
// rss_feeds.error_msg。
type RSSFetcher interface {
	Fetch(ctx context.Context, url string) (*gofeed.Feed, error)
}

// rssHTTPTimeout 单次抓取的总超时（含连接、读取、解析）。
const rssHTTPTimeout = 30 * time.Second

// rssUserAgent 用于伪装 RSS 抓取请求，避免 Go 默认 UA 被反爬拦截。
const rssUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

// sharedRSSClient 保存按配置指纹缓存的 HTTP 客户端。
//
// UA、代理、超时都属于可热更新配置。若只在启动时构建一次，
// 改完配置必须重启才生效；这里用指纹判断是否重建。
type sharedRSSClient struct {
	client      *req.Client
	fingerprint string
}

// 包级单例：抓取与图片下载共用同一连接池，避免重复建连。
// imageClient 是图片专用的严格版本：连接层校验目标 IP。
var (
	rssClientMu        sync.Mutex
	rssClientInst      sharedRSSClient
	rssImageClientInst sharedRSSClient
)

// rssClientFingerprint 计算影响客户端行为的配置指纹。
func rssClientFingerprint(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	ua := cfg.RSS.UserAgent
	if ua == "" {
		ua = rssUserAgent
	}
	return fmt.Sprintf("%s|%v", ua, cfg.RSS.ProxyNames)
}

// rssHTTPClient 返回与当前配置匹配的共享 HTTP 客户端（用于抓取 feed）。
func rssHTTPClient() *req.Client { return rssClient(false) }

// rssImageHTTPClient 返回图片下载用的严格客户端。
//
// 与 rssHTTPClient 的差别：连接层校验解析后的 IP，重定向逐跳校验。
// RSS 内容里的图片 URL 完全由第三方控制，比用户自己配置的 feed URL
// 危险得多，因此单独加严。配置（UA/代理）与抓取保持一致。
func rssImageHTTPClient() *req.Client { return rssClient(true) }

// rssClient 返回与当前配置匹配的共享客户端。
func rssClient(strict bool) *req.Client {
	cfg := config.Get()
	fp := rssClientFingerprint(cfg)
	if strict {
		fp += "|strict"
	}

	rssClientMu.Lock()
	defer rssClientMu.Unlock()

	inst := &rssClientInst
	if strict {
		inst = &rssImageClientInst
	}
	if inst.client != nil && inst.fingerprint == fp {
		return inst.client
	}
	*inst = sharedRSSClient{client: buildRSSHTTPClientWith(cfg, strict), fingerprint: fp}
	return inst.client
}

// httpFeedFetcher 直接通过 HTTP 抓取订阅源，是默认实现。
type httpFeedFetcher struct{}

// NewHTTPFeedFetcher 创建一个直接抓取的 RSSFetcher。
func NewHTTPFeedFetcher() RSSFetcher { return httpFeedFetcher{} }

// Fetch 抓取并解析订阅源。
func (httpFeedFetcher) Fetch(ctx context.Context, urlStr string) (*gofeed.Feed, error) {
	// 每次抓取前复验 URL（防止 DNS rebinding 绕过创建时的校验）
	if err := validateFeedURL(urlStr); err != nil {
		return nil, fmt.Errorf("订阅源 URL 校验失败: %w", err)
	}

	// 调用方未设置截止时间时补默认预算；已设置则尊重调用方的更短预算。
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, rssHTTPTimeout)
		defer cancel()
	}

	resp, err := rssHTTPClient().R().SetContext(ctx).Get(urlStr)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP 状态码 %d", resp.StatusCode)
	}

	feed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RSS 解析失败: %w", err)
	}

	return feed, nil
}

// buildRSSHTTPClient 根据配置构建 HTTP 客户端。
//
// 通过 config.ResolveProxy 解析 RSS 模块的代理配置，支持 HTTP 代理；
// 配置为空时直连。strict=true 时额外加连接层 IP 校验（图片下载用）。
func buildRSSHTTPClientWith(cfg *config.Config, strict bool) *req.Client {
	// 代理构建统一走 config.TransportFor：它按请求 scheme 选择
	// http_url / https_url，并支持 socks5。
	var proxyNames []string
	if cfg != nil {
		proxyNames = cfg.RSS.ProxyNames
	}
	transport, err := config.TransportFor(proxyNames)
	if err != nil {
		// 代理配置非法时退回直连，不让抓取整体不可用。
		logger.Warn("[rss] 代理配置不可用，改为直连", "error", err)
		transport, _ = config.TransportFor(nil)
	}

	client := req.C().SetTimeout(rssHTTPTimeout).DisableDebugLog()
	if strict {
		transport = guardDial(transport)
	}
	client.GetClient().Transport = transport

	ua := rssUserAgent
	if cfg != nil && cfg.RSS.UserAgent != "" {
		ua = cfg.RSS.UserAgent
	}
	client.SetCommonHeader("User-Agent", ua)

	// 限制重定向次数，并校验每一跳目标（防 SSRF 借重定向绕过 IP 校验）
	client.GetClient().CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("超过最大重定向次数")
		}
		if strict {
			// 图片可能被重定向到内网：每一跳都做带 DNS 的完整校验。
			if err := validateImageURL(req.Context(), req.URL.String(), imageAllowPrivate()); err != nil {
				return fmt.Errorf("重定向目标校验失败: %w", err)
			}
			return nil
		}
		if err := validateFeedURL(req.URL.String()); err != nil {
			return fmt.Errorf("重定向目标校验失败: %w", err)
		}
		return nil
	}

	return client
}

// guardDial 给直连 transport 加连接层 IP 校验。
//
// 为什么需要：只在请求前校验 URL 无法防住两件事——
//   - 重定向：初始 URL 合法，302 指向内网；
//   - DNS rebinding：校验时解析到公网 IP，真正建连时解析到内网 IP。
//
// 这里在每次建连时重新解析并校验，且直接把连接打到已校验的 IP，
// 避免“校验完再解析一次”的窗口。
//
// 走 HTTP 代理时 transport.Proxy 非空，拨号目标是代理本身（可能是内网），
// 此时不做 IP 校验——目标解析发生在代理侧，HomeHub 无法干预，这是使用
// 代理的固有边界；重定向仍逐跳校验。SOCKS5 的拨号目标就是目标地址，
// 校验照常生效。
func guardDial(transport *http.Transport) *http.Transport {
	if transport.Proxy != nil {
		return transport
	}

	base := transport.DialContext
	if base == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		base = dialer.DialContext
	}

	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if imageAllowPrivate() {
			return base(ctx, network, addr)
		}

		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		var ip net.IP
		if parsed := net.ParseIP(host); parsed != nil {
			ip = parsed
		} else {
			ips, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, host)
			if lookupErr != nil {
				return nil, fmt.Errorf("解析目标主机失败: %w", lookupErr)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("目标主机无可用地址: %s", host)
			}
			// 所有解析结果都必须合法，避免多 A 记录里混入内网地址。
			for _, candidate := range ips {
				if isBlockedIP(candidate.IP) {
					return nil, fmt.Errorf("目标主机解析到内部地址: %s", candidate.IP)
				}
			}
			ip = ips[0].IP
		}
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("禁止连接到内部地址: %s", ip)
		}

		// 直接 dial 已校验的 IP，消除“校验后再解析”的窗口。
		return base(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	return transport
}
