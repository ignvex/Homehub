package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/imroc/req/v3"
	"gorm.io/gorm"
)

const (
	gravatarHTTPTimeout = 10 * time.Second
	// gravatarDefaultBaseURL 是 Gravatar 头像服务地址。
	// 抽成字段是为了让测试指向 httptest，避免单元测试依赖外网。
	gravatarDefaultBaseURL = "https://www.gravatar.com/avatar"
)

// ErrGravatarDisabled 表示配置中关闭了 Gravatar 头像功能。
//
// Handler 应把它映射为 404，前端 UserAvatar 已实现 onError 降级为
// 首字母占位，因此关闭后界面仍然可用。
var ErrGravatarDisabled = errors.New("Gravatar 头像功能已禁用")

// enabled 报告当前配置是否启用了 Gravatar。
//
// 配置未加载时按禁用处理：避免在配置异常时对外发起请求。
func gravatarEnabled() bool {
	cfg := config.Get()
	return cfg != nil && cfg.Gravatar.Enabled
}

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
	// baseURL 可通过 SetBaseURL 替换，默认 gravatarDefaultBaseURL。
	baseURL string
}

// NewGravatarService 创建 GravatarService 实例。
func NewGravatarService(db *gorm.DB) *GravatarService {
	return &GravatarService{
		db:         db,
		httpClient: buildGravatarHTTPClient(),
		baseURL:    gravatarDefaultBaseURL,
	}
}

// SetBaseURL 替换 Gravatar 服务地址。仅供测试使用。
func (s *GravatarService) SetBaseURL(u string) {
	s.baseURL = u
}

// URL 根据邮箱生成 Gravatar 头像的外部 URL（仅供下载使用）。
func (s *GravatarService) URL(email string) string {
	cfg := config.Get()
	if cfg == nil || !cfg.Gravatar.Enabled {
		return ""
	}
	hash := (&model.User{Email: email}).GravatarHash()
	if hash == "" {
		return ""
	}
	base := s.baseURL
	if base == "" {
		base = gravatarDefaultBaseURL
	}
	return fmt.Sprintf("%s/%s?s=%d&d=%s&r=%s",
		base, hash, cfg.Gravatar.DefaultSize, cfg.Gravatar.DefaultStyle, cfg.Gravatar.Rating)
}

// GetAvatarData 从缓存获取用户头像的二进制数据和 MIME 类型。
//
// 如果缓存不存在，自动从 Gravatar 下载并缓存后返回。
// 若用户无 email 或 Gravatar 不可达，返回错误。
func (s *GravatarService) GetAvatarData(userID uint64) ([]byte, string, error) {
	if !gravatarEnabled() {
		return nil, "", ErrGravatarDisabled
	}

	// 1. 查用户（需要邮箱判断缓存是否仍然有效）
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return nil, "", fmt.Errorf("查询用户失败: %w", err)
	}
	if user.Email == "" {
		return nil, "", errors.New("用户未设置邮箱")
	}

	// 2. 查缓存：仅在邮箱未变时可用
	//
	// 之前这里命中即返回，导致改了邮箱后头像永远是旧的
	// （EmailHash 只写入、从不比对）。现在用 EmailHash 校验，
	// 邮箱变更时自动重新下载。
	var cache model.AvatarCache
	err := s.db.Where("user_id = ?", userID).First(&cache).Error
	if err == nil {
		if cache.EmailHash == user.GravatarHash() {
			return cache.ImageData, cache.ContentType, nil
		}
		// 邮箱已变更：继续往下走，重新下载并覆盖缓存
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", fmt.Errorf("查询头像缓存失败: %w", err)
	}

	// 3. 下载并缓存
	cached, err := s.downloadAndCache(&user)
	if err != nil {
		return nil, "", err
	}
	return cached.ImageData, cached.ContentType, nil
}

// RefreshAvatar 检查 Gravatar 是否有更新，若有则重新下载。
//
// 返回 true 表示头像已更新；返回 false 表示无需更新或检查失败。
// 若缓存不存在则直接下载。
func (s *GravatarService) RefreshAvatar(userID uint64) (bool, error) {
	if !gravatarEnabled() {
		return false, ErrGravatarDisabled
	}

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
			return false, fmt.Errorf("gravatar HEAD 请求失败: %w", err)
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
		return nil, fmt.Errorf("gravatar 返回状态码 %d", resp.StatusCode)
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
	}

	// UPSERT：先硬删后插，避免软删除记录占用唯一索引
	s.db.Unscoped().Where("user_id = ?", user.ID).Delete(&model.AvatarCache{})
	if err := s.db.Create(&cache).Error; err != nil {
		return nil, fmt.Errorf("保存头像缓存失败: %w", err)
	}

	return &cache, nil
}

// buildGravatarHTTPClient 根据代理配置构建 HTTP 客户端。
//
// 通过 config.ResolveProxy 获取代理列表，支持 HTTP 代理（通过 Transport.Proxy）
// 和 SOCKS5 代理（通过 DialContext）。配置为空时直连。
func buildGravatarHTTPClient() *req.Client {
	// 代理构建统一走 config.TransportFor：按请求 scheme 选择
	// http_url / https_url，并支持 socks5。
	var proxyNames []string
	if cfg := config.Get(); cfg != nil {
		proxyNames = cfg.Gravatar.ProxyNames
	}
	transport, err := config.TransportFor(proxyNames)
	if err != nil {
		logger.Warn("[gravatar] 代理配置不可用，改为直连", "error", err)
		transport, _ = config.TransportFor(nil)
	}

	client := req.C().SetTimeout(gravatarHTTPTimeout)
	client.GetClient().Transport = transport
	return client
}
