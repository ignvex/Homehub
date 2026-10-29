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

// remoteFetcher 通过外部 extension 服务抓取订阅源。
//
// 使用场景：HomeHub 所在网络访问某些源很慢或经常被限流，
// 而 extension 部署在网络条件更好的位置（或已集中缓存了结果）。
type remoteFetcher struct {
	client *http.Client
}

// NewRemoteFetcher 创建向 extension 发请求的 RSSFetcher。
func NewRemoteFetcher(timeout time.Duration) RSSFetcher {
	if timeout <= 0 {
		timeout = rssHTTPTimeout
	}
	return &remoteFetcher{
		client: &http.Client{
			Timeout: timeout,
			// 不跟随重定向：extension 地址由运维配置，跳转不应发生
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// remoteResponse 是 extension 可能返回的 JSON 包装。
type remoteResponse struct {
	Feed string `json:"feed"`
}

// Fetch 向 extension 请求并解析订阅源。
func (f *remoteFetcher) Fetch(ctx context.Context, feedURL string) (*gofeed.Feed, error) {
	cfg := config.Get()
	if cfg == nil || cfg.RSS.ExtensionURL == "" {
		return nil, fmt.Errorf("extension 地址未配置")
	}

	// 复用投递 URL 校验：extension 收到的是同一批地址，校验口径必须一致
	if err := validateFeedURL(feedURL); err != nil {
		return nil, fmt.Errorf("订阅源 URL 校验失败: %w", err)
	}

	endpoint := strings.TrimRight(cfg.RSS.ExtensionURL, "/") + "/fetch?url=" + url.QueryEscape(feedURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 extension 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/xml, application/rss+xml, application/atom+xml, application/json")
	if cfg.RSS.ExtensionToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.RSS.ExtensionToken)
	}

	resp, err := f.client.Do(req)
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

	return parseExtensionBody(body, resp.Header.Get("Content-Type"))
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
	if cfg == nil || cfg.RSS.ExtensionURL == "" {
		return "local"
	}
	return fmt.Sprintf("remote|%s|%s|%d", cfg.RSS.ExtensionURL, cfg.RSS.ExtensionToken, cfg.RSS.FetchTimeoutSeconds)
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
		logger.Info("[rss] 抓取交由 extension 处理", "url", cfg.RSS.ExtensionURL)
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
