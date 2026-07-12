package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/imroc/req/v3"
	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"golang.org/x/net/proxy"
	"gorm.io/gorm"
)

const (
	gravatarBaseURL     = "https://www.gravatar.com/avatar"
	gravatarHTTPTimeout = 10 * time.Second
)

// GravatarService Gravatar 头像业务服务。
//
// 负责：
//   - 根据用户邮箱生成 Gravatar URL
//   - 从 Gravatar 下载头像并缓存到数据库
//   - 从缓存读取头像数据（所有对外访问均走缓存）
//   - HEAD 请求检测更新（ETag/Last-Modified）
type GravatarService struct {
	db         *gorm.DB
	httpClient *req.Client
}

// NewGravatarService 创建 GravatarService 实例。
func NewGravatarService(db *gorm.DB) *GravatarService {
	return &GravatarService{
		db:         db,
		httpClient: buildGravatarHTTPClient(),
	}
}

// URL 根据邮箱生成 Gravatar 头像的外部 URL（仅供下载使用）。
func (s *GravatarService) URL(email string) string {
	u := &model.User{Email: email}
	cfg := config.Get()
	if cfg == nil {
		return ""
	}
	return u.GravatarURL(cfg.Gravatar.DefaultSize, cfg.Gravatar.DefaultStyle, cfg.Gravatar.Rating)
}

// GetAvatarData 从缓存获取用户头像的二进制数据和 MIME 类型。
//
// 如果缓存不存在，自动从 Gravatar 下载并缓存后返回。
// 若用户无 email 或 Gravatar 不可达，返回错误。
func (s *GravatarService) GetAvatarData(userID uint64) ([]byte, string, error) {
	// 1. 先查缓存
	var cache model.AvatarCache
	err := s.db.Where("user_id = ?", userID).First(&cache).Error
	if err == nil {
		return cache.ImageData, cache.ContentType, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", fmt.Errorf("查询头像缓存失败: %w", err)
	}

	// 2. 查用户邮箱
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return nil, "", fmt.Errorf("查询用户失败: %w", err)
	}
	if user.Email == "" {
		return nil, "", errors.New("用户未设置邮箱")
	}

	// 3. 下载并缓存
	cached, err := s.downloadAndCache(&user)
	if err != nil {
		return nil, "", err
	}
	return cached.ImageData, cached.ContentType, nil
}

// EnsureCached 确保用户的头像已缓存（不存在则下载）。
//
// 返回缓存记录指针。若用户无 email 返回 nil, nil。
func (s *GravatarService) EnsureCached(user *model.User) (*model.AvatarCache, error) {
	if user.Email == "" {
		return nil, nil
	}

	var cache model.AvatarCache
	err := s.db.Where("user_id = ?", user.ID).First(&cache).Error
	if err == nil {
		return &cache, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("查询头像缓存失败: %w", err)
	}

	return s.downloadAndCache(user)
}

// RefreshAvatar 检查 Gravatar 是否有更新，若有则重新下载。
//
// 返回 true 表示头像已更新；返回 false 表示无需更新或检查失败。
// 若缓存不存在则直接下载。
func (s *GravatarService) RefreshAvatar(userID uint64) (bool, error) {
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return false, fmt.Errorf("查询用户失败: %w", err)
	}
	if user.Email == "" {
		return false, errors.New("用户未设置邮箱")
	}

	gravatarURL := s.URL(user.Email)

	var cache model.AvatarCache
	cacheExists := s.db.Where("user_id = ?", userID).First(&cache).Error == nil

	// 如果缓存存在，发 HEAD 请求检查更新
	if cacheExists {
		r := s.httpClient.R()
		if cache.ETag != "" {
			r.SetHeader("If-None-Match", cache.ETag)
		}
		if cache.LastModified != "" {
			r.SetHeader("If-Modified-Since", cache.LastModified)
		}

		resp, err := r.Head(gravatarURL)
		if err != nil {
			return false, fmt.Errorf("Gravatar HEAD 请求失败: %w", err)
		}
		resp.Body.Close()

		// 304 Not Modified → 无需更新
		if resp.StatusCode == http.StatusNotModified {
			return false, nil
		}
	}

	// 重新下载
	_, err := s.downloadAndCache(&user)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ClearCache 清除指定用户的头像缓存。
//
// 当用户 email 变更时应调用此方法使旧缓存失效。
func (s *GravatarService) ClearCache(userID uint64) error {
	result := s.db.Where("user_id = ?", userID).Delete(&model.AvatarCache{})
	if result.Error != nil {
		return fmt.Errorf("清除头像缓存失败: %w", result.Error)
	}
	return nil
}

// ============================================================
// 内部方法
// ============================================================

// downloadAndCache 从 Gravatar 下载头像并写入数据库缓存。
func (s *GravatarService) downloadAndCache(user *model.User) (*model.AvatarCache, error) {
	gravatarURL := s.URL(user.Email)
	if gravatarURL == "" {
		return nil, errors.New("无法生成 Gravatar URL")
	}

	resp, err := s.httpClient.R().Get(gravatarURL)
	if err != nil {
		return nil, fmt.Errorf("下载 Gravatar 头像失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gravatar 返回状态码 %d", resp.StatusCode)
	}

	imageData, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 限制 5MB
	if err != nil {
		return nil, fmt.Errorf("读取 Gravatar 响应失败: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg" // 默认
	}

	cache := model.AvatarCache{
		UserID:       user.ID,
		EmailHash:    user.GravatarHash(),
		ImageData:    imageData,
		ContentType:  contentType,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FileSize:     int64(len(imageData)),
	}

	// UPSERT：先删后插，确保唯一
	s.db.Where("user_id = ?", user.ID).Delete(&model.AvatarCache{})
	if err := s.db.Create(&cache).Error; err != nil {
		return nil, fmt.Errorf("保存头像缓存失败: %w", err)
	}

	return &cache, nil
}

// buildGravatarHTTPClient 根据代理配置构建 HTTP 客户端。
//
// 支持 HTTP 代理（通过 Transport.Proxy）和 SOCKS5 代理（通过 DialContext）。
// 配置的解析逻辑由 config.ResolveProxy 提供。
func buildGravatarHTTPClient() *req.Client {
	proxyType, proxyAddr := resolveGravatarProxy()

	transport := &http.Transport{}

	switch proxyType {
	case "socks5":
		dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
		if err == nil {
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
		// SOCKS5 dialer 创建失败时退回到直连

	default: // "http" 或空
		if proxyAddr != "" {
			transport.Proxy = func(_ *http.Request) (*url.URL, error) {
				return url.Parse(proxyAddr)
			}
		}
	}

	client := req.C().
		SetTimeout(gravatarHTTPTimeout)
	client.GetClient().Transport = transport
	return client
}

// resolveGravatarProxy 解析 Gravatar 代理配置，返回 (type, address)。
//
// 兼容旧的 ProxyName 单代理字段，优先使用 custom_proxy_url。
// 返回空字符串表示不使用代理（直连）。
func resolveGravatarProxy() (string, string) {
	cfg := config.Get()
	if cfg == nil {
		return "", ""
	}

	// 优先：自定义代理 URL
	if cfg.Gravatar.CustomProxyURL != "" {
		return "http", cfg.Gravatar.CustomProxyURL
	}

	// 全局开关关闭 → 直连
	if !cfg.ProxyEnabled {
		return "", ""
	}

	// Gravatar 未启用代理 → 直连
	if !cfg.Gravatar.UseProxy || cfg.Gravatar.ProxyName == "" {
		return "", ""
	}

	// 在 Proxy 列表中按名称查找
	for _, p := range cfg.Proxy {
		if p.Name == cfg.Gravatar.ProxyName && p.Enabled {
			addr := p.HTTPURL
			if addr == "" {
				continue
			}
			pType := p.Type
			if pType == "" {
				pType = "http"
			}
			return pType, addr
		}
	}

	return "", ""
}
