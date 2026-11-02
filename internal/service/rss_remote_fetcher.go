package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/hubclient"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/mmcdole/gofeed"
)

// 通信方向说明
//
// HomeHub 是唯一发起方：它对 extension 发 HTTP 请求，拿回解析结果。
// extension 不连接 HomeHub、不持有数据库凭据、不推送数据。
// 这样边界清晰：extension 挂了只会让抓取变慢，不会影响其他模块。
//
// 请求形状（GET）：
//
//	GET {extension_url}/fetch?url=<urlencoded feed url>
//	Authorization: Bearer <extension_token>   // token 非空时
//	Accept: application/xml, application/rss+xml, application/atom+xml
//
// 期望响应（任选其一）：
//   - 原始 RSS/Atom XML，Content-Type 为 XML 类
//   - JSON：{"feed": "<原始 XML 字符串>"} 或直接是 gofeed 的 JSON 结构
//
// 失败约定：非 2xx 视为抓取失败，错误信息会写入 rss_feeds.error_msg。

// remoteExtensionConcurrency 限制同时打到 Extension 的 /fetch 请求数。
//
// 与 rss.fetch_concurrency 不是一回事：后者是本地抓取时同时处理几个源，
// 每个源一次 HTTP 往返即可；代抓时每个源还多一层 Extension 内部排队，
// 而 /fetch 是同步阻塞的。按本地并发（默认 4）一起打过去，Extension 那边
// 只会排队甚至 429，宿主看到的却只是「慢」。
//
// 2 是保守值：Extension 自己的 rss.fetch_concurrency 也有限制，两层并发
// 相乘才是它真实承受的压力。真要更快，应当调高 Extension 侧的并发上限，
// 而不是把这里放开。
const remoteExtensionConcurrency = 2

// remoteExtensionSlots 是进程级信号量，跨 autoFetcher 重建仍然有效。
var remoteExtensionSlots = make(chan struct{}, remoteExtensionConcurrency)

// remoteFetcher 通过 HomeHub Extension 抓取订阅源。
//
// 使用场景：HomeHub 所在网络访问某些源很慢或经常被限流，
// 而 Extension 部署在网络条件更好的位置。条目仍然解析回 HomeHub，
// 存在本地数据库——Extension 只负责“把字节取回来”。
type remoteFetcher struct {
	timeout time.Duration
}

// NewRemoteFetcher 创建向 extension 发请求的 RSSFetcher。
func NewRemoteFetcher(timeout time.Duration) RSSFetcher {
	if timeout <= 0 {
		timeout = rssHTTPTimeout
	}
	return &remoteFetcher{timeout: timeout}
}

// remoteResponse 是 extension 可能返回的 JSON 包装。
type remoteResponse struct {
	Feed string `json:"feed"`
}

// Fetch 向 extension 请求并解析订阅源。
func (f *remoteFetcher) Fetch(ctx context.Context, feedURL string) (*gofeed.Feed, error) {
	cfg := config.Get()

	// 复用投递 URL 校验：extension 收到的是同一批地址，校验口径必须一致
	if err := validateFeedURL(feedURL); err != nil {
		return nil, fmt.Errorf("订阅源 URL 校验失败: %w", err)
	}

	if cfg == nil || !cfg.Extension.Features.Fetch {
		return nil, fmt.Errorf("extension.features.fetch 未启用")
	}

	client, err := buildExtensionClient(cfg, f.timeout)
	if err != nil {
		return nil, fmt.Errorf("extension 连接不可用: %w", err)
	}

	// 拿到槽位才发请求。ctx 取消时立即放弃，避免在关停期间卡住。
	select {
	case remoteExtensionSlots <- struct{}{}:
		defer func() { <-remoteExtensionSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	q := url.Values{}
	q.Set("url", feedURL)

	resp, err := client.DoRaw(ctx, http.MethodGet, "/fetch", q.Encode(),
		"application/xml, application/rss+xml, application/atom+xml, application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("extension 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 走统一错误结构而不是「状态码 + 前 512 字节」。
		//
		// 后者会把 JSON 原文写进 error_msg：既占掉 rss_feeds.error_msg 的
		// 1024 字符预算，也丢掉两个关键信息——协议错误码与 request_id。
		// 没有 request_id 就没法和 Extension 的日志对上，「为什么这条失败」
		// 只能靠时间戳猜。
		apiErr := hubclient.ErrorFromResponse(resp)
		return nil, fmt.Errorf("extension 抓取失败: %w", apiErr)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 上限 32MB
	if err != nil {
		return nil, fmt.Errorf("读取 extension 响应失败: %w", err)
	}

	// 先拦住 HTML。这是部署配错时最常见的形式：地址指向一个
	// 没有 /fetch 的服务（比如一个普通的 SPA 站点），对方用 200 返回
	// 首页 HTML，而「XML 解析失败」这种错误会把排查方向引到 XML 本身。
	if looksLikeHTML(body, resp.Header.Get("Content-Type")) {
		return nil, fmt.Errorf(
			"extension 返回的是 HTML 而不是 feed：%s 上可能没有 /fetch 端点。"+
				"检查 extension.base_url 是否指向实现了 extension 契约的服务",
			cfg.Extension.BaseURL)
	}

	return parseExtensionBody(body, resp.Header.Get("Content-Type"))
}

// looksLikeHTML 判断响应是否为一个 HTML 页面。
//
// 只看嗅探结果不看声明：很多 SPA 的 fallback 会把 Content-Type 写成
// text/html，但也不排有服务声明错的。两者任一命中都算 HTML——
// 一个 feed 不可能是 HTML，所以这里没有误判风险。
func looksLikeHTML(body []byte, contentType string) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	sniffed := strings.ToLower(http.DetectContentType(head))
	return strings.HasPrefix(sniffed, "text/html")
}

// parseExtensionBody 兼容 XML 与 JSON 两种响应形态。
func parseExtensionBody(body []byte, contentType string) (*gofeed.Feed, error) {
	trimmed := strings.TrimSpace(string(body))
	isJSON := strings.Contains(contentType, "json") ||
		(len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '['))

	if !isJSON {
		feed, err := gofeed.NewParser().ParseString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("extension 返回的 XML 解析失败: %w", err)
		}
		return feed, nil
	}

	// JSON 形态：优先取 {"feed": "<xml>"}，否则尝试直接按 gofeed 结构解析
	var wrapper remoteResponse
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Feed != "" {
		feed, err := gofeed.NewParser().ParseString(wrapper.Feed)
		if err != nil {
			return nil, fmt.Errorf("extension 返回的 feed 字段解析失败: %w", err)
		}
		return feed, nil
	}

	var feed gofeed.Feed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("extension 返回的 JSON 无法解析: %w", err)
	}
	return &feed, nil
}

// autoFetcher 按当前配置在本地抓取与 extension 之间自动切换。
//
// 配置可热更新：URL/token/超时变化时重建底层实现，无需重启。
// 这是 NewRSSService 的默认抓取器；SetFetcher 可覆盖它（测试或特殊部署）。
type autoFetcher struct {
	mu      sync.Mutex
	current RSSFetcher
	key     string
}

// newAutoFetcher 创建一个跟随配置的抓取器。
func newAutoFetcher() *autoFetcher { return &autoFetcher{} }

// fetcherKey 是影响实现选择的配置指纹。
func fetcherKey(cfg *config.Config) string {
	if cfg == nil || !cfg.Extension.Enabled || !cfg.Extension.Features.Fetch || cfg.Extension.BaseURL == "" {
		return "local"
	}
	// 私钥也要进指纹：轮换后必须重建客户端，否则会继续用旧身份签名。
	return fmt.Sprintf("remote|%s|%s|%d",
		cfg.Extension.BaseURL, cfg.Extension.PrivateKeyPEM, cfg.RSS.FetchTimeoutSeconds)
}

// pick 返回与当前配置匹配的实现，必要时重建。
func (a *autoFetcher) pick() RSSFetcher {
	cfg := config.Get()
	key := fetcherKey(cfg)

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.current != nil && a.key == key {
		return a.current
	}

	if key == "local" {
		a.current = NewHTTPFeedFetcher()
	} else {
		logger.Info("[rss] 抓取交由 extension 处理", "url", cfg.Extension.BaseURL)
		timeout := rssHTTPTimeout
		if cfg.RSS.FetchTimeoutSeconds > 0 {
			timeout = time.Duration(cfg.RSS.FetchTimeoutSeconds) * time.Second
		}
		a.current = NewRemoteFetcher(timeout)
	}
	a.key = key
	return a.current
}

// Fetch 委托给当前选中的实现。
func (a *autoFetcher) Fetch(ctx context.Context, url string) (*gofeed.Feed, error) {
	return a.pick().Fetch(ctx, url)
}
