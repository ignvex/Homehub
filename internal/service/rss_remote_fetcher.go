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

	q := url.Values{}
	q.Set("url", feedURL)

	resp, err := client.DoRaw(ctx, http.MethodGet, "/fetch", q.Encode(),
		"application/xml, application/rss+xml, application/atom+xml, application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("extension 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 限制读取量，避免异常响应把错误信息撑爆
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("extension 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
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
