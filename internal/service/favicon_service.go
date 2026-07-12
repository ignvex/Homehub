package service

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/imroc/req/v3"
	"gorm.io/gorm"
)

const (
	googleFaviconBaseURL = "https://www.google.com/s2/favicons"
	faviconHTTPTimeout   = 5 * time.Second
)

// FaviconService 网站图标业务服务。
//
// 负责：
//   - 根据书签 URL 提取域名
//   - 通过 Google S2 favicon 服务获取站点图标 URL
//   - 支持代理（HTTP 代理，由 config.ResolveProxy 解析）
type FaviconService struct {
	db         *gorm.DB
	httpClient *req.Client
}

// NewFaviconService 创建 FaviconService 实例。
func NewFaviconService(db *gorm.DB) *FaviconService {
	return &FaviconService{
		db:         db,
		httpClient: buildFaviconHTTPClient(),
	}
}

// FetchFavicon 根据书签 URL 获取网站图标 URL。
//
// 先从 URL 中提取域名，构造 Google S2 favicon 请求地址，
// 然后发起 HEAD 请求验证图标是否存在（状态码 200），
// 存在则返回 favicon URL，否则返回空字符串。
// 单次请求超时 5 秒。
func (s *FaviconService) FetchFavicon(bookmarkURL string) (string, error) {
	if bookmarkURL == "" {
		return "", nil
	}

	// 提取域名
	parsed, err := url.Parse(bookmarkURL)
	if err != nil {
		return "", fmt.Errorf("解析 URL 失败: %w", err)
	}
	domain := parsed.Host
	if domain == "" {
		return "", nil
	}

	// 构造 Google S2 favicon URL
	faviconURL := fmt.Sprintf("%s?domain=%s&sz=32", googleFaviconBaseURL, domain)

	// HEAD 请求验证图标是否存在
	resp, err := s.httpClient.R().Head(faviconURL)
	if err != nil {
		return "", nil // 网络不可达，返回空
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return faviconURL, nil
	}

	return "", nil
}

// ============================================================
// 内部方法
// ============================================================

// buildFaviconHTTPClient 根据书签代理配置构建 HTTP 客户端。
//
// 通过 config.ResolveProxy 解析书签模块的代理配置，
// 支持 HTTP 代理。配置为空时直连。
func buildFaviconHTTPClient() *req.Client {
	transport := &http.Transport{}

	cfg := config.Get()
	if cfg != nil {
		proxyURLs := config.ResolveProxy(cfg.Bookmark.ProxyServiceConfig, "")
		if len(proxyURLs) > 0 {
			proxyAddr := proxyURLs[0]
			transport.Proxy = func(_ *http.Request) (*url.URL, error) {
				return url.Parse(proxyAddr)
			}
		}
	}

	client := req.C().SetTimeout(faviconHTTPTimeout)
	client.GetClient().Transport = transport
	return client
}
