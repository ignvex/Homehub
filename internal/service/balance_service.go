package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/imroc/req/v3"
)

// ============================================================
// 类型定义
// ============================================================

// TargetResult 单个监控点的查询结果（内存缓存，不入库）。
type TargetResult struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	BaseURL       string    `json:"base_url"`
	Enabled       bool      `json:"enabled"`
	Balance       float64   `json:"balance"`              // 剩余余额
	Currency      string    `json:"currency"`             // 货币单位，如 "CNY" / "USD" / "quota"
	LastCheckedAt time.Time `json:"last_checked_at"`      // 最后查询时间
	LastError     string    `json:"last_error,omitempty"` // 最近错误信息
	Status        string    `json:"status"`               // "ok" / "error" / "checking"
}

// BalanceStatus 监控模块整体状态。
type BalanceStatus struct {
	Enabled     bool      `json:"enabled"`
	TargetCount int       `json:"target_count"`
	OKCount     int       `json:"ok_count"`
	ErrorCount  int       `json:"error_count"`
	LastRefresh time.Time `json:"last_refresh_at"`
}

// ============================================================
// BalanceService
// ============================================================

// BalanceService 余额监控服务。
//
// 不依赖数据库，所有余额结果仅存于内存缓存中。
// 后台 goroutine 按配置间隔定时刷新所有启用的监控点。
type BalanceService struct {
	mu          sync.RWMutex
	results     map[string]*TargetResult // key = target Name
	client      *req.Client
	stopCh      chan struct{}
	lastRefresh time.Time
}

// NewBalanceService 创建余额监控服务实例。
func NewBalanceService() *BalanceService {
	transport := &http.Transport{
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     30 * time.Second,
	}
	client := req.C().
		SetTimeout(15 * time.Second).
		SetCommonHeaders(map[string]string{
			"User-Agent": "HomeHub/1.0",
		})
	client.GetClient().Transport = transport

	return &BalanceService{
		results: make(map[string]*TargetResult),
		client:  client,
		stopCh:  make(chan struct{}),
	}
}

// ============================================================
// 后台刷新
// ============================================================

// StartBackgroundRefresh 启动后台定时刷新。
//
// ctx 用于生命周期控制（如程序退出时取消）。
// 刷新间隔由 config.json 中的 balance_monitor.refresh_interval_seconds 控制。
func (s *BalanceService) StartBackgroundRefresh(ctx context.Context) {
	cfg := config.Get()
	if cfg == nil || !cfg.BalanceMonitor.Enabled {
		logger.Info("[balance] 余额监控未启用，跳过后台刷新")
		return
	}

	interval := time.Duration(cfg.BalanceMonitor.RefreshIntervalSeconds) * time.Second
	if interval < 10*time.Second {
		interval = 60 * time.Second
	}

	logger.Info("[balance] 余额监控后台刷新已启动", "interval", interval.String())

	// 立即执行一次刷新
	s.refreshAll()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("[balance] 余额监控后台刷新已停止")
				return
			case <-s.stopCh:
				logger.Info("[balance] 余额监控后台刷新已停止")
				return
			case <-ticker.C:
				// 重新读取配置（支持热加载）
				cfg := config.Get()
				if cfg == nil || !cfg.BalanceMonitor.Enabled {
					continue
				}
				s.refreshAll()
			}
		}
	}()
}

// Stop 停止后台刷新。
func (s *BalanceService) Stop() {
	select {
	case s.stopCh <- struct{}{}:
	default:
	}
}

// ============================================================
// 公开接口
// ============================================================

// GetAllResults 返回所有监控点的缓存结果快照。
func (s *BalanceService) GetAllResults() []TargetResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]TargetResult, 0, len(s.results))
	for _, r := range s.results {
		results = append(results, *r)
	}
	return results
}

// GetStatus 返回监控模块整体状态。
func (s *BalanceService) GetStatus() BalanceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := BalanceStatus{
		LastRefresh: s.lastRefresh,
	}
	for _, r := range s.results {
		if !r.Enabled {
			continue
		}
		status.TargetCount++
		if r.Status == "ok" {
			status.OKCount++
		} else {
			status.ErrorCount++
		}
	}

	cfg := config.Get()
	if cfg != nil {
		status.Enabled = cfg.BalanceMonitor.Enabled
	}
	return status
}

// RefreshAll 手动触发全量刷新（同步执行，返回最新结果）。
func (s *BalanceService) RefreshAll() []TargetResult {
	s.refreshAll()
	return s.GetAllResults()
}

// ============================================================
// 内部刷新逻辑
// ============================================================

func (s *BalanceService) refreshAll() {
	cfg := config.Get()
	if cfg == nil {
		return
	}

	targets := cfg.BalanceMonitor.Targets
	if len(targets) == 0 {
		return
	}

	// 先填充/更新缓存条目（基于配置），标记为 checking
	s.mu.Lock()
	for _, t := range targets {
		entry, exists := s.results[t.Name]
		if !exists {
			entry = &TargetResult{
				Name:    t.Name,
				Type:    t.Type,
				BaseURL: t.BaseURL,
			}
			s.results[t.Name] = entry
		}
		entry.Enabled = t.Enabled
		entry.Type = t.Type
		entry.BaseURL = t.BaseURL
		if t.Enabled {
			entry.Status = "checking"
		}
	}
	s.mu.Unlock()

	// 逐个查询启用的目标
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		s.fetchOne(t)
	}

	s.mu.Lock()
	s.lastRefresh = time.Now()
	s.mu.Unlock()
}

func (s *BalanceService) fetchOne(t config.BalanceTarget) {
	var (
		balance  float64
		currency string
		err      error
	)

	switch strings.ToLower(t.Type) {
	case "deepseek":
		balance, currency, err = s.fetchDeepSeek(t)
	default:
		err = fmt.Errorf("不支持的类型: %s（目前仅支持 deepseek）", t.Type)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.results[t.Name]
	if !exists {
		return
	}

	entry.LastCheckedAt = time.Now()
	if err != nil {
		entry.Status = "error"
		entry.LastError = err.Error()
		logger.Warn("[balance] 查询失败", "name", t.Name, "type", t.Type, "error", err)
	} else {
		entry.Status = "ok"
		entry.Balance = balance
		entry.Currency = currency
		entry.LastError = ""
		logger.Info("[balance] 查询成功", "name", t.Name, "type", t.Type,
			"balance", fmt.Sprintf("%.2f", balance), "currency", currency)
	}
}

// ============================================================
// DeepSeek API 调用
// ============================================================

// deepseekBalanceResp DeepSeek /user/balance 响应结构。
type deepseekBalanceResp struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		Currency        string `json:"currency"`
		TotalBalance    string `json:"total_balance"`
		GrantedBalance  string `json:"granted_balance"`
		ToppedUpBalance string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

func (s *BalanceService) fetchDeepSeek(t config.BalanceTarget) (float64, string, error) {
	url := strings.TrimRight(t.BaseURL, "/") + "/user/balance"

	resp, err := s.client.R().
		SetHeader("Authorization", "Bearer "+t.APIKey).
		Get(url)
	if err != nil {
		return 0, "", fmt.Errorf("HTTP 请求失败: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var dsResp deepseekBalanceResp
	if err := json.Unmarshal(body, &dsResp); err != nil {
		return 0, "", fmt.Errorf("解析响应失败: %w", err)
	}

	if !dsResp.IsAvailable {
		return 0, "", fmt.Errorf("账户不可用")
	}
	if len(dsResp.BalanceInfos) == 0 {
		return 0, "CNY", nil
	}

	var total float64
	fmt.Sscanf(dsResp.BalanceInfos[0].TotalBalance, "%f", &total)
	currency := dsResp.BalanceInfos[0].Currency
	if currency == "" {
		currency = "CNY"
	}
	return total, currency, nil
}
