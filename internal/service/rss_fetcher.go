package service

import (
	"context"
	"fmt"
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
var (
	rssClientMu   sync.Mutex
	rssClientInst sharedRSSClient
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

// rssHTTPClient 返回与当前配置匹配的共享 HTTP 客户端。
func rssHTTPClient() *req.Client {
	cfg := config.Get()
	fp := rssClientFingerprint(cfg)

	rssClientMu.Lock()
	defer rssClientMu.Unlock()

	if rssClientInst.client != nil && rssClientInst.fingerprint == fp {
		return rssClientInst.client
	}
	rssClientInst = sharedRSSClient{client: buildRSSHTTPClient(cfg), fingerprint: fp}
	return rssClientInst.client
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
// 配置为空时直连。
func buildRSSHTTPClient(cfg *config.Config) *req.Client {
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
		if err := validateFeedURL(req.URL.String()); err != nil {
			return fmt.Errorf("重定向目标校验失败: %w", err)
		}
		return nil
	}

	return client
}
