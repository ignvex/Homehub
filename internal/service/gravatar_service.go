package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

const (
	// gravatarMinSize/gravatarMaxSize 限制缓存的像素尺寸。
	// 上限 512 覆盖当前所有显示位置（最大 112px @2x），
	// 也避免配置成 1024 时每张头像都占几百 KB。
	gravatarMinSize = 16
	gravatarMaxSize = 512
	// gravatarUnspecifiedSize 是配置异常时的兜底尺寸。
	gravatarUnspecifiedSize = 256
	// gravatarNegativeTTL 是「上游确认无头像」的进程内缓存时长。
	// 没有它时 default_style=404 会让每次请求都打一次上游。
	gravatarNegativeTTL = 5 * time.Minute
	// gravatarUpstreamConcurrency 限制同时进行的上游下载数，
	// 防止公开头像接口被枚举时打爆 Gravatar 或 extension。
	gravatarUpstreamConcurrency = 8
	// gravatarNegativeEntries 是负缓存的最大条目数。
	gravatarNegativeEntries = 1024
)

// Avatar 是可直接返回给 HTTP 层的头像数据。
type Avatar struct {
	Data        []byte
	ContentType string
	// ETag 由缓存变体与邮箱哈希派生：任一方变化都会生成新值，
	// 因此可以安全地用于条件请求。
	ETag string
}

// GravatarService Gravatar 头像业务服务。
//
// 负责：
//   - 按「用户 + 来源 + 尺寸 + 风格 + 分级」缓存头像
//   - 上游选择（官方 Gravatar / extension）随配置热更新
//   - 并发合并、负缓存、上游并发上限
//   - 管理员刷新时的条件请求与失效清理
//
// 不负责响应体校验 —— 那是 avatarSource 的职责，所有来源共用同一套规则。
type GravatarService struct {
	db *gorm.DB

	// sourceFactory 构建上游实现；为 nil 时使用 newAvatarSource。
	// 单独留出这一层是为了让单元测试能指向 httptest，而不依赖外网。
	sourceFactory func(*config.Config) avatarSource

	mu     sync.Mutex
	source avatarSource
	srcKey string

	negMu sync.Mutex
	neg   map[string]time.Time

	// sf 合并同一变体的并发下载；upstream 限制全局上游并发。
	sf       singleflight.Group
	upstream chan struct{}
}

// NewGravatarService 创建 GravatarService 实例。
//
// 上游客户端在首次请求时按配置构建，因此构造顺序不依赖 config.Load。
func NewGravatarService(db *gorm.DB) *GravatarService {
	return &GravatarService{
		db:       db,
		neg:      make(map[string]time.Time),
		upstream: make(chan struct{}, gravatarUpstreamConcurrency),
	}
}

// GetAvatarData 获取指定用户的头像。
//
// 优先命中缓存；未命中时下载并写入缓存。同一用户的并发请求只会触发
// 一次上游下载。尺寸由配置的 default_size 决定，所有显示位置共用一张图。
func (s *GravatarService) GetAvatarData(ctx context.Context, userID uint64) (*Avatar, error) {
	cfg, err := enabledGravatarConfig()
	if err != nil {
		return nil, err
	}

	user, err := s.loadUser(userID)
	if err != nil {
		return nil, err
	}
	if user.Email == "" {
		return nil, ErrAvatarUnavailable
	}

	source := s.sourceFor(cfg)
	req := avatarRequest{
		Hash:   user.GravatarHash(),
		Size:   resolveAvatarSize(cfg),
		Style:  strings.ToLower(cfg.Gravatar.DefaultStyle),
		Rating: strings.ToLower(cfg.Gravatar.Rating),
	}
	variant := avatarVariant(source, req.Size, req.Style, req.Rating)
	emailHash := req.Hash
	flightKey := fmt.Sprintf("%d|%s|%s", user.ID, variant, emailHash)

	// 快路径：缓存命中时不进入 singleflight，避免高频读取排队。
	if avatar, err := s.loadCachedAvatar(user.ID, variant, emailHash); err == nil {
		return avatar, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if s.recentlyMissing(flightKey) {
		return nil, ErrAvatarNotFound
	}

	result, err, _ := s.sf.Do(flightKey, func() (any, error) {
		// 等锁期间可能已有协程完成下载，重新检查一次。
		if avatar, err := s.loadCachedAvatar(user.ID, variant, emailHash); err == nil {
			return avatar, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		payload, _, err := s.fetchUpstream(ctx, cfg, source, req, nil)
		if err != nil {
			if errors.Is(err, ErrAvatarNotFound) {
				s.markMissing(flightKey)
			}
			// 客户端主动断开（如关闭页面）不是服务异常，不刷告警日志。
			if ctx.Err() == nil {
				logger.Warn("[gravatar] 下载头像失败", "user_id", user.ID, "variant", variant, "error", err)
			}
			return nil, err
		}

		if err := s.storeAvatar(user.ID, variant, emailHash, payload); err != nil {
			return nil, err
		}
		return &Avatar{
			Data:        payload.Data,
			ContentType: payload.ContentType,
			ETag:        avatarETag(variant, emailHash, payload.Data),
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*Avatar), nil
}

// RefreshAvatar 检查并更新该用户所有已缓存的头像变体。
//
// 返回 true 表示缓存发生了变化（重新下载或清除了失效变体）；
// false 表示全部未变化。配置或邮箱变更后遗留的旧变体会被清除。
// 若该用户还没有任何缓存，则按默认尺寸下载一份。
func (s *GravatarService) RefreshAvatar(ctx context.Context, userID uint64) (bool, error) {
	cfg, err := enabledGravatarConfig()
	if err != nil {
		return false, err
	}

	user, err := s.loadUser(userID)
	if err != nil {
		return false, err
	}
	if user.Email == "" {
		return false, ErrAvatarUnavailable
	}

	source := s.sourceFor(cfg)
	emailHash := user.GravatarHash()

	var caches []model.AvatarCache
	if err := s.db.Where("user_id = ?", user.ID).Find(&caches).Error; err != nil {
		return false, fmt.Errorf("查询头像缓存失败: %w", err)
	}

	req := avatarRequest{
		Hash:   emailHash,
		Size:   resolveAvatarSize(cfg),
		Style:  strings.ToLower(cfg.Gravatar.DefaultStyle),
		Rating: strings.ToLower(cfg.Gravatar.Rating),
	}
	variant := avatarVariant(source, req.Size, req.Style, req.Rating)

	// 没有缓存：直接下载。
	if len(caches) == 0 {
		payload, _, err := s.fetchUpstream(ctx, cfg, source, req, nil)
		if err != nil {
			return false, err
		}
		return true, s.storeAvatar(user.ID, variant, emailHash, payload)
	}

	cache := caches[0]
	sourceKey, size, style, rating, ok := parseAvatarVariant(cache.Variant)

	// 来源、邮箱或参数已变：这一行不会再被命中，无需条件请求，直接重下。
	if !ok || sourceKey != source.Key() || cache.EmailHash != emailHash {
		// 变体可解析时沿用其中的尺寸与风格，否则保持当前配置。
		// 不这样做会在变体损坏时把空字符串当作 d=/r= 发给上游。
		if ok {
			req.Size = clampInt(size, gravatarMinSize, gravatarMaxSize)
			req.Style = style
			req.Rating = rating
		}
		payload, _, err := s.fetchUpstream(ctx, cfg, source, req, nil)
		if errors.Is(err, ErrAvatarNotFound) {
			if err := s.deleteAvatar(cache.ID); err != nil {
				return false, err
			}
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return true, s.storeAvatar(user.ID, avatarVariant(source, req.Size, req.Style, req.Rating), emailHash, payload)
	}

	payload, notModified, err := s.fetchUpstream(ctx, cfg, source, req,
		&avatarConditions{ETag: cache.ETag, LastModified: cache.LastModified})
	switch {
	case errors.Is(err, ErrAvatarNotFound):
		if err := s.deleteAvatar(cache.ID); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	case notModified:
		return false, nil
	default:
		return true, s.storeAvatar(user.ID, variant, emailHash, payload)
	}
}

// ============================================================
// 内部方法
// ============================================================

// enabledGravatarConfig 返回启用状态下的配置快照。
//
// 配置未加载时按禁用处理：避免在配置异常时对外发起请求。
func enabledGravatarConfig() (*config.Config, error) {
	cfg := config.Get()
	if cfg == nil || !cfg.Gravatar.Enabled {
		return nil, ErrGravatarDisabled
	}
	return cfg, nil
}

// loadUser 读取用户；不存在时返回 ErrUserNotFound 而不是 GORM 原始错误。
func (s *GravatarService) loadUser(userID uint64) (*model.User, error) {
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return &user, nil
}

// sourceFor 返回与当前配置匹配的上游实现，配置变化时重建。
func (s *GravatarService) sourceFor(cfg *config.Config) avatarSource {
	key := avatarSourceKey(cfg)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.source != nil && s.srcKey == key {
		return s.source
	}
	if s.sourceFactory != nil {
		s.source = s.sourceFactory(cfg)
	} else {
		s.source = newAvatarSource(cfg)
	}
	s.srcKey = key
	logger.Info("[gravatar] 上游已就绪",
		"via_extension", cfg.Extension.Enabled && cfg.Extension.Features.Avatar,
		"timeout_seconds", cfg.Gravatar.TimeoutSeconds)
	return s.source
}

// avatarSourceKey 是影响上游实现与网络行为的配置指纹。
func avatarSourceKey(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	// 指纹要覆盖所有会改变上游行为与网络路径的配置：换 Extension
	// 开关、换地址、换私钥、换全局代理，都应让缓存的 source 失效。
	return fmt.Sprintf("%t|%s|%s|%d|%s",
		cfg.Extension.Enabled,
		cfg.Extension.BaseURL,
		cfg.Extension.PrivateKeyPEM,
		cfg.Gravatar.TimeoutSeconds,
		strings.Join(cfg.Proxy.Default, ","),
	)
}

// loadCachedAvatar 读取仍然有效的缓存。
//
// 邮箱哈希与变体都必须匹配：前者处理改邮箱，后者处理改尺寸/风格/来源。
// 比对放在 Go 侧而不只依赖 SQL：条件写错时宁可重新下载，也不返回旧图。
func (s *GravatarService) loadCachedAvatar(userID uint64, variant, emailHash string) (*Avatar, error) {
	var cache model.AvatarCache
	if err := s.db.Where("user_id = ?", userID).First(&cache).Error; err != nil {
		return nil, err
	}
	if cache.Variant != variant || cache.EmailHash != emailHash {
		return nil, gorm.ErrRecordNotFound
	}
	return &Avatar{
		Data:        cache.ImageData,
		ContentType: cache.ContentType,
		ETag:        avatarETag(variant, emailHash, cache.ImageData),
	}, nil
}

// storeAvatar 以事务方式覆盖该用户的缓存。
//
// 先物理删除再插入：历史版本可能留下软删除行，它们仍会占用 user_id
// 的唯一索引。放入事务保证插入失败时旧记录不被删掉，
// 不会出现「缓存凭空消失、下次请求又要打上游」。
func (s *GravatarService) storeAvatar(userID uint64, variant, emailHash string, payload *avatarPayload) error {
	cache := model.AvatarCache{
		UserID:       userID,
		Variant:      variant,
		EmailHash:    emailHash,
		ImageData:    payload.Data,
		ContentType:  payload.ContentType,
		ETag:         payload.ETag,
		LastModified: payload.LastModified,
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().
			Where("user_id = ?", userID).
			Delete(&model.AvatarCache{}).Error; err != nil {
			return fmt.Errorf("清理旧头像缓存失败: %w", err)
		}
		if err := tx.Create(&cache).Error; err != nil {
			return fmt.Errorf("保存头像缓存失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 新图已经落库，之前记录的「上游无头像」不再成立。
	s.clearMissing(userID)
	return nil
}

// deleteAvatar 物理删除一条缓存。
//
// 用 Unscoped 是为了释放唯一索引；软删除行仍然占键，会让后续插入失败。
func (s *GravatarService) deleteAvatar(id uint64) error {
	if err := s.db.Unscoped().Delete(&model.AvatarCache{}, id).Error; err != nil {
		return fmt.Errorf("删除头像缓存失败: %w", err)
	}
	return nil
}

// fetchUpstream 带全局并发上限与独立超时地调用上游。
//
// 使用 WithoutCancel：singleflight 中第一个调用者断开连接时，
// 其他等待者不应该一起收到 context canceled。
func (s *GravatarService) fetchUpstream(ctx context.Context, cfg *config.Config, source avatarSource, req avatarRequest, known *avatarConditions) (*avatarPayload, bool, error) {
	select {
	case s.upstream <- struct{}{}:
		defer func() { <-s.upstream }()
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}

	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gravatarTimeout(cfg)+time.Second)
	defer cancel()

	return source.Fetch(fetchCtx, req, known)
}

// gravatarTimeout 返回上游请求超时，配置缺失时用 10 秒。
func gravatarTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.Gravatar.TimeoutSeconds > 0 {
		return time.Duration(cfg.Gravatar.TimeoutSeconds) * time.Second
	}
	return 10 * time.Second
}

// resolveAvatarSize 返回缓存实际使用的像素尺寸。
//
// 取配置的 default_size，并限制在 16-512：超出范围的配置（例如 1024）
// 只会让数据库与浏览器多做无用的缩放工作。
func resolveAvatarSize(cfg *config.Config) int {
	size := gravatarUnspecifiedSize
	if cfg != nil && cfg.Gravatar.DefaultSize > 0 {
		size = cfg.Gravatar.DefaultSize
	}
	return clampInt(size, gravatarMinSize, gravatarMaxSize)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// avatarVariant 构造可解析的缓存变体："来源|尺寸|风格|分级"。
//
// 用可读格式而不是纯哈希，排查时能直接看出缓存属于哪组参数。
func avatarVariant(source avatarSource, size int, style, rating string) string {
	return fmt.Sprintf("%s|%d|%s|%s", source.Key(), size, strings.ToLower(style), strings.ToLower(rating))
}

// parseAvatarVariant 解析缓存变体，格式不合法时 ok=false。
func parseAvatarVariant(variant string) (sourceKey string, size int, style, rating string, ok bool) {
	parts := strings.SplitN(variant, "|", 4)
	if len(parts) != 4 || parts[0] == "" {
		return "", 0, "", "", false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n <= 0 {
		return "", 0, "", "", false
	}
	return parts[0], n, parts[2], parts[3], true
}

// avatarETag 派生对外 ETag。加引号符合 RFC 7232 的实体标签语法。
//
// 必须把图片内容一起纳入：只用 (variant, emailHash) 派生时，上游换了头像
// 而用户的邮箱与配置都没变，刷新后 ETag 与刷新前完全相同。浏览器对
// `Cache-Control: no-cache` 的响应会存副本并带 If-None-Match 重验，
// 于是永远拿到 304，继续显示旧图——而服务端已经返回 updated:true，
// 表现为一个"假成功"。
func avatarETag(variant, emailHash string, data []byte) string {
	h := sha256.New()
	h.Write([]byte(variant))
	h.Write([]byte{0})
	h.Write([]byte(emailHash))
	h.Write([]byte{0})
	h.Write(data)
	return `"` + hex.EncodeToString(h.Sum(nil)[:8]) + `"`
}

// recentlyMissing 报告负缓存是否仍然有效。
func (s *GravatarService) recentlyMissing(key string) bool {
	s.negMu.Lock()
	defer s.negMu.Unlock()

	expires, ok := s.neg[key]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(s.neg, key)
		return false
	}
	return true
}

// clearMissing 清除某个用户的负缓存（例如手动刷新成功后）。
func (s *GravatarService) clearMissing(userID uint64) {
	prefix := strconv.FormatUint(userID, 10) + "|"

	s.negMu.Lock()
	defer s.negMu.Unlock()
	for key := range s.neg {
		if strings.HasPrefix(key, prefix) {
			delete(s.neg, key)
		}
	}
}

// markMissing 记录「上游确认无头像」，避免反复请求。
func (s *GravatarService) markMissing(key string) {
	s.negMu.Lock()
	defer s.negMu.Unlock()

	if len(s.neg) >= gravatarNegativeEntries {
		now := time.Now()
		for k, expires := range s.neg {
			if now.After(expires) {
				delete(s.neg, k)
			}
		}
		// 仍然满：整体清空比无界增长更安全，负缓存只是优化。
		if len(s.neg) >= gravatarNegativeEntries {
			s.neg = make(map[string]time.Time)
		}
	}
	s.neg[key] = time.Now().Add(gravatarNegativeTTL)
}
