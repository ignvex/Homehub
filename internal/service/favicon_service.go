package service

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
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
// 先从 URL 中提取域名，校验域名合法性（防 SSRF），
// 构造 Google S2 favicon 请求地址，然后发起 HEAD 请求验证图标是否存在。
// 返回 favicon URL 或空字符串。
func (s *FaviconService) FetchFavicon(bookmarkURL string) (string, error) {
	if bookmarkURL == "" {
		return "", nil
	}

	// 提取域名
	parsed, err := url.Parse(bookmarkURL)
	if err != nil {
		return "", nil
	}
	domain := parsed.Host
	if domain == "" {
		return "", nil
	}

	// SSRF 防护：校验域名合法性
	if err := validateFaviconDomain(domain); err != nil {
		return "", nil
	}

	// 构造 Google S2 favicon URL
	faviconURL := fmt.Sprintf("%s?domain=%s&sz=32", googleFaviconBaseURL, domain)

	// HEAD 请求验证图标是否存在
	resp, err := s.httpClient.R().Head(faviconURL)
	if err != nil {
		return "", nil
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

// validateFaviconDomain 校验域名合法性，防止 SSRF 攻击。
//
// 禁止内网 IP、回环地址、云元数据端点及 localhost 域名。
func validateFaviconDomain(host string) error {
	// 提取主机名（去掉端口）
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	// 解析 IP
	ip := net.ParseIP(hostname)
	if ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("禁止访问内部地址: %s", hostname)
		}
		if ip.IsPrivate() {
			return fmt.Errorf("禁止访问内网地址: %s", hostname)
		}
		if ip.Equal(net.IPv4(169, 254, 169, 254)) {
			return fmt.Errorf("禁止访问云元数据端点")
		}
		return nil
	}

	// 主机名黑名单
	lowerHost := strings.ToLower(hostname)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".local") {
		return fmt.Errorf("禁止访问本地主机名")
	}

	return nil
}

// buildFaviconHTTPClient 根据书签代理配置构建 HTTP 客户端。
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
