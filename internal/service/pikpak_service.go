package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
)

// ============================================================
// PikPak 磁力链接辅助
// ============================================================
//
// 职责边界刻意收得很窄：**只做提交，不做轮询与文件管理**。
// HomeHub 把磁力链接交给 PikPak 离线下载，记下任务 ID 与结果；
// 之后 PikPak 那边怎么样，由 PikPak 自己管。
//
// 这样即使 PikPak 改了接口，坏的也只是“提交”这一个动作，
// 不会牵连条目列表、抓取、清理等主流程。
//
// ⚠️ 真实实现基于 PikPak 公开的 web API（signin + files）。该接口
// 非官方文档化，可能随时变更；因此：
//   - 全部逻辑走 PikPakClient 接口，可注入替身；
//   - 失败只记录、不重试、不阻塞抓取；
//   - 错误信息截断后存库，便于事后诊断。

// ErrPikPakDisabled 表示配置里没有启用 PikPak 集成。
var ErrPikPakDisabled = errors.New("PikPak 集成未启用")

// ErrNoMagnet 表示条目没有磁力链接可提交。
var ErrNoMagnet = errors.New("该条目没有磁力链接")

// ErrMagnetNotLocked 表示提交前要求先锁定磁力链接。
var ErrMagnetNotLocked = errors.New("磁力链接尚未锁定，请先锁定再提交")

// PikPakClient 是提交磁力链接的能力抽象。
//
// 返回 taskID 供调用方记录。实现必须尊重 ctx 的超时与取消。
type PikPakClient interface {
	Submit(ctx context.Context, magnet string) (taskID string, err error)
}

// pikpakHTTPClient 是实现 PikPakClient 的默认 HTTP 客户端。
//
// token 缓存在内存里：PikPak 的 access_token 有效期约数小时，
// 每次提交都重新登录会明显变慢，也更容易触发风控。
type pikpakHTTPClient struct {
	baseURL      string
	username     string
	password     string
	clientID     string
	clientSecret string
	http         *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

const (
	// pikpakTokenTTL 是 token 的保守缓存时长。
	//
	// 比上游实际有效期短：宁可多登录一次，也不要在边界上拿着过期 token
	// 去提交，那会返回一个看起来像“磁力链接无效”的误导性错误。
	pikpakTokenTTL = 30 * time.Minute
	// pikpakMaxErrLen 是存库错误信息的长度上限（对应列 varchar(512)）。
	pikpakMaxErrLen = 480
)

// defaultPikPakEndpoint 是配置留空时使用的地址。
const defaultPikPakEndpoint = "https://api-drive.mypikpak.com"

// defaultPikPakTimeout 是配置留空时使用的超时。
const defaultPikPakTimeout = 15 * time.Second

// EffectivePikPakEndpoint 返回实际生效的地址（含默认值）。
//
// 单独暴露它，是为了让状态查询接口显示“真的会用哪个地址”，
// 而不是把配置里的空串原样透出去、让人以为没配置就没生效。
func EffectivePikPakEndpoint(cfg config.PikPakConfig) string {
	if ep := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/"); ep != "" {
		return ep
	}
	return defaultPikPakEndpoint
}

// EffectivePikPakTimeout 返回实际生效的超时。
func EffectivePikPakTimeout(cfg config.PikPakConfig) time.Duration {
	if cfg.TimeoutSeconds > 0 {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return defaultPikPakTimeout
}

// NewPikPakClient 根据配置创建客户端；未启用时返回 nil。
func NewPikPakClient(cfg config.PikPakConfig) PikPakClient {
	if !cfg.Enabled {
		return nil
	}
	endpoint := EffectivePikPakEndpoint(cfg)
	timeout := EffectivePikPakTimeout(cfg)
	return &pikpakHTTPClient{
		baseURL:      endpoint,
		username:     cfg.Username,
		password:     cfg.Password,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		http:         &http.Client{Timeout: timeout},
	}
}

// Submit 登录（如需）并把磁力链接作为离线下载任务提交。
func (c *pikpakHTTPClient) Submit(ctx context.Context, magnet string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(magnet), "magnet:") {
		return "", fmt.Errorf("不是磁力链接: %.40s", magnet)
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(map[string]any{
		"kind":        "drive#file",
		"name":        magnetName(magnet),
		"upload_type": "UPLOAD_TYPE_URL",
		"url":         magnet,
	})
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/drive/v1/files?type=", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("提交失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("PikPak 返回 %d: %s", resp.StatusCode, truncateErr(string(raw)))
	}

	var parsed struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// 提交可能已经成功，只是响应结构不同。不把这种情况当成失败：
		// 返回空 taskID 让调用方记为 saved，但日志里留痕。
		logger.Warn("[pikpak] 响应无法解析，按已提交处理", "body", truncateErr(string(raw)))
		return "", nil
	}
	if parsed.Task.ID != "" {
		return parsed.Task.ID, nil
	}
	return parsed.ID, nil
}

// accessToken 返回可用的 access_token，必要时重新登录。
func (c *pikpakHTTPClient) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}

	body, err := json.Marshal(map[string]string{
		"client_id":     "YUMx5nI8ZU8Ap8pm",
		"client_secret": "dbw2OtmVEeuUvIptb1Coyg",
		"username":      c.username,
		"password":      c.password,
	})
	if err != nil {
		return "", fmt.Errorf("序列化登录请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/auth/signin", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("构造登录请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("登录失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("登录返回 %d: %s", resp.StatusCode, truncateErr(string(raw)))
	}

	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("登录响应缺少 access_token")
	}

	c.token = out.AccessToken
	c.tokenExp = time.Now().Add(pikpakTokenTTL)
	return c.token, nil
}

// magnetName 从磁力链接里取一个可读名字（dn 参数）。
//
// PikPak 用它作为离线任务名。取不到就用固定前缀加哈希片段，
// 避免所有任务都叫 "magnet"。
func magnetName(magnet string) string {
	if i := strings.Index(magnet, "dn="); i >= 0 {
		rest := magnet[i+3:]
		if j := strings.IndexAny(rest, "&"); j >= 0 {
			rest = rest[:j]
		}
		if rest != "" {
			return truncateErr(rest)
		}
	}
	// btih 是磁力链接的内容哈希，取前 8 位作为名字后缀。
	if i := strings.Index(magnet, "btih:"); i >= 0 {
		h := magnet[i+5:]
		if j := strings.IndexAny(h, "&"); j >= 0 {
			h = h[:j]
		}
		if len(h) > 8 {
			h = h[:8]
		}
		if h != "" {
			return "magnet-" + h
		}
	}
	return "magnet-task"
}

// truncateErr 把错误信息压到列宽以内。
func truncateErr(s string) string {
	return model.TruncateRunes(strings.TrimSpace(s), pikpakMaxErrLen)
}

// ============================================================
// RSSService 上的锁定与提交操作
// ============================================================

// LockMagnet 锁定条目的磁力链接。
//
// 幂等：已经锁定的条目重复锁定不报错，只刷新时间与操作者。
func (s *RSSService) LockMagnet(entryID uint64, operator *model.User) error {
	if operator == nil {
		return ErrForbidden
	}

	now := time.Now()
	operatorID := uint64(0)
	if operator != nil {
		operatorID = operator.ID
	}

	res := s.db.Model(&model.RSSEntry{}).
		Where("id = ?", entryID).
		Updates(map[string]any{
			"magnet_locked":    true,
			"magnet_locked_at": now,
			"magnet_locked_by": operatorID,
		})
	if res.Error != nil {
		return fmt.Errorf("锁定磁力链接失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrRSSEntryNotFound
	}
	logger.Info("[rss] 磁力链接已锁定", "entry_id", entryID, "operator_id", operatorID)
	return nil
}

// UnlockMagnet 解除锁定。
//
// 只清锁定标记，不动 PikPak 提交状态 —— 已经提交到 PikPak 的任务
// 是既成事实，解除锁定不代表撤销那次提交。
func (s *RSSService) UnlockMagnet(entryID uint64) error {
	res := s.db.Model(&model.RSSEntry{}).
		Where("id = ?", entryID).
		Updates(map[string]any{
			"magnet_locked":    false,
			"magnet_locked_at": nil,
			"magnet_locked_by": 0,
		})
	if res.Error != nil {
		return fmt.Errorf("解除磁力锁定失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrRSSEntryNotFound
	}
	return nil
}

// SubmitMagnetToPikPak 把条目的磁力链接提交给 PikPak。
//
// 前置条件：条目必须有磁力链接，且已锁定。要求先锁定是有意的 ——
// 提交是外部副作用（往别人账号里塞下载任务），不该对一条随时可能被
// 抓取改写的链接做。锁定让人先确认“就是这条”。
//
// 失败会把错误写进 pikpak_status / pikpak_error，便于在列表里看到；
// 不返回错误给抓取流程（这个动作是辅助性的，不该阻塞主流程）。
func (s *RSSService) SubmitMagnetToPikPak(ctx context.Context, entryID uint64) (*model.RSSEntry, error) {
	if s.pikpak == nil {
		return nil, ErrPikPakDisabled
	}

	var entry model.RSSEntry
	if err := s.db.First(&entry, entryID).Error; err != nil {
		return nil, ErrRSSEntryNotFound
	}
	if strings.TrimSpace(entry.MagnetURL) == "" {
		return nil, ErrNoMagnet
	}
	if !entry.MagnetLocked {
		return nil, ErrMagnetNotLocked
	}

	taskID, err := s.pikpak.Submit(ctx, entry.MagnetURL)
	now := time.Now()
	if err != nil {
		msg := truncateErr(err.Error())
		s.db.Model(&model.RSSEntry{}).Where("id = ?", entryID).
			Updates(map[string]any{
				"pikpak_status": "failed",
				"pikpak_error":  msg,
			})
		logger.Warn("[rss] 提交 PikPak 失败", "entry_id", entryID, "error", err)
		entry.PikPakStatus = "failed"
		entry.PikPakError = msg
		return &entry, fmt.Errorf("提交 PikPak 失败: %w", err)
	}

	s.db.Model(&model.RSSEntry{}).Where("id = ?", entryID).
		Updates(map[string]any{
			"pikpak_status":   "saved",
			"pikpak_task_id":  model.TruncateRunes(taskID, 128),
			"pikpak_error":    "",
			"pikpak_saved_at": now,
		})
	logger.Info("[rss] 已提交 PikPak", "entry_id", entryID, "task_id", taskID)

	entry.PikPakStatus = "saved"
	entry.PikPakTaskID = taskID
	entry.PikPakSavedAt = &now
	entry.PikPakError = ""
	return &entry, nil
}

// SetPikPakClient 注入 PikPak 客户端（测试与配置热更新用）。
func (s *RSSService) SetPikPakClient(c PikPakClient) {
	s.pikpak = c
}
