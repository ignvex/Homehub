package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/imroc/req/v3"
)

// ============================================================
// 类型定义
// ============================================================

// 余额监控的业务错误。
var (
	ErrBalanceTargetNotFound = errors.New("监控点不存在")
	ErrBalanceTargetExists   = errors.New("同名监控点已存在")
	ErrBalanceInvalidType    = errors.New("不支持的供应商类型")
	ErrBalanceMissingField   = errors.New("缺少必填字段")
)

// TargetResult 单个监控点的查询结果（内存缓存，不入库）。
//
// ID 是缓存的主键而不是 Name：监控点可以改名，而改名不该让历史结果
// 漂到另一条记录上。
type TargetResult struct {
	ID            uint64    `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	BaseURL       string    `json:"base_url"`
	Enabled       bool      `json:"enabled"`
	Balance       float64   `json:"balance"`              // 剩余余额
	Currency      string    `json:"currency"`             // 货币单位，如 "CNY" / "USD" / "quota"
	LastCheckedAt time.Time `json:"last_checked_at"`      // 最后查询时间
	LastError     string    `json:"last_error,omitempty"` // 最近错误信息
	Status        string    `json:"status"`               // "ok" / "error" / "checking" / "idle"
}

// BalanceStatus 监控模块整体状态。
type BalanceStatus struct {
	Enabled     bool      `json:"enabled"`
	TargetCount int       `json:"target_count"`
	OKCount     int       `json:"ok_count"`
	ErrorCount  int       `json:"error_count"`
	LastRefresh time.Time `json:"last_refresh_at"`
}

// BalanceTargetView 是对外暴露的监控点，含脱敏后的密钥与最新结果。
//
// 与 model.BalanceTarget 的区别只有一处但很关键：APIKey 不在其中，
// 只给掩码与「有没有配」的布尔。密钥一旦出现在响应里，浏览器缓存、
// 开发者工具、日志中间件都可能把它复制走。
type BalanceTargetView struct {
	ID        uint64 `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	BaseURL   string `json:"base_url"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`

	HasAPIKey    bool   `json:"has_api_key"`
	APIKeyMasked string `json:"api_key_masked,omitempty"`

	// 以下是运行时结果，来自内存缓存；从未刷新过时为零值。
	Balance       float64   `json:"balance"`
	Currency      string    `json:"currency,omitempty"`
	Status        string    `json:"status"`
	LastError     string    `json:"last_error,omitempty"`
	LastCheckedAt time.Time `json:"last_checked_at"`
}

// BalanceTargetInput 创建与更新共用的请求体。
//
// 指针字段区分「没传」与「传了零值」：更新时不传 enabled 与显式传
// false 是两件事，前者是「别动」，后者是「停用」。
type BalanceTargetInput struct {
	Name      *string `json:"name"`
	Type      *string `json:"type"`
	BaseURL   *string `json:"base_url"`
	APIKey    *string `json:"api_key"`
	Enabled   *bool   `json:"enabled"`
	SortOrder *int    `json:"sort_order"`
}

// ============================================================
// BalanceService
// ============================================================

// BalanceService 余额监控服务。
//
// 监控点存在数据库里（balance_targets），余额结果只在内存缓存中——
// 余额是易变的外部状态，落库只会得到一份过时的副本。
// 后台 goroutine 按配置间隔刷新所有启用的监控点。
type BalanceService struct {
	db *gorm.DB

	mu          sync.RWMutex
	results     map[uint64]*TargetResult // key = target ID
	client      *req.Client
	lastRefresh time.Time
}

// NewBalanceService 创建余额监控服务实例。
func NewBalanceService(db *gorm.DB) *BalanceService {
	// base_url 由编辑者填写，等同于一个可配置的出网目标：连接层校验
	// 解析后的 IP，默认拒绝内网/回环/链路本地，防止被用来探测内网。
	transport := guardDialWith(&http.Transport{
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     30 * time.Second,
	}, balanceAllowPrivate)
	client := req.C().
		SetTimeout(15 * time.Second).
		SetCommonHeaders(map[string]string{
			"User-Agent": rssUserAgent,
		})
	client.GetClient().Transport = transport

	return &BalanceService{
		db:      db,
		results: make(map[uint64]*TargetResult),
		client:  client,
	}
}

// balanceAllowPrivate 返回是否放行内网监控点（热更新生效，默认 false）。
func balanceAllowPrivate() bool {
	cfg := config.Get()
	return cfg != nil && cfg.BalanceMonitor.AllowPrivateHosts
}

// balanceErrorBodyLogLimit 是失败响应体写入 debug 日志的最大字符数。
const balanceErrorBodyLogLimit = 512

// ============================================================
// 迁移：config.json → 数据库
// ============================================================

// MigrateTargetsFromConfig 把 config.json 里的监控点导入数据库。
//
// 只在**表为空**时导入，且不清空 config.json 里的原值：
//
//   - 表非空说明用户已经在 Web 端管理过，此时再导入会造出重复条目。
//   - 不清空 config 是因为那是用户的文件，且保留原值让回滚可行——
//     删掉数据库里的行就能回到旧行为。
//
// 导入后 config 的 targets 不再被读取，日志会明确说出来，避免有人继续
// 改配置文件然后困惑于「为什么没生效」。
func (s *BalanceService) MigrateTargetsFromConfig() error {
	if s.db == nil {
		return nil
	}
	cfg := config.Get()
	if cfg == nil || len(cfg.BalanceMonitor.Targets) == 0 {
		return nil
	}

	var count int64
	if err := s.db.Model(&model.BalanceTarget{}).Count(&count).Error; err != nil {
		return fmt.Errorf("统计监控点失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	targets := make([]model.BalanceTarget, 0, len(cfg.BalanceMonitor.Targets))
	for i, t := range cfg.BalanceMonitor.Targets {
		// 配置校验已经保证 name/type/base_url/api_key 非空且类型受支持，
		// 这里不再做兵底：留着只会让人以为空值能被容忍。
		targets = append(targets, model.BalanceTarget{
			Name:      strings.TrimSpace(t.Name),
			Type:      strings.ToLower(strings.TrimSpace(t.Type)),
			BaseURL:   strings.TrimRight(strings.TrimSpace(t.BaseURL), "/"),
			APIKey:    t.APIKey,
			Enabled:   t.Enabled,
			SortOrder: i,
		})
	}

	if err := s.db.Create(&targets).Error; err != nil {
		return fmt.Errorf("导入监控点失败: %w", err)
	}
	logger.Info("[balance] 已把 config.json 中的监控点导入数据库",
		"count", len(targets),
		"note", "balance_monitor.targets 从此不再生效，请在 Web 端管理监控点")
	return nil
}

// ============================================================
// 监控点 CRUD
// ============================================================

// ListTargets 返回全部监控点（按 sort_order、id 排序），合并最新缓存结果。
func (s *BalanceService) ListTargets() ([]BalanceTargetView, error) {
	var rows []model.BalanceTarget
	if err := s.db.Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询监控点失败: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]BalanceTargetView, 0, len(rows))
	for i := range rows {
		out = append(out, s.viewOf(&rows[i]))
	}
	return out, nil
}

// GetTarget 读取单条监控点（含明文密钥，仅供内部使用）。
func (s *BalanceService) GetTarget(id uint64) (*model.BalanceTarget, error) {
	var t model.BalanceTarget
	if err := s.db.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBalanceTargetNotFound
		}
		return nil, fmt.Errorf("查询监控点失败: %w", err)
	}
	return &t, nil
}

// CreateTarget 新建一个监控点。
func (s *BalanceService) CreateTarget(in BalanceTargetInput) (*BalanceTargetView, error) {
	t := model.BalanceTarget{Enabled: true}
	if err := applyBalanceInput(&t, in, true); err != nil {
		return nil, err
	}
	if err := validateBalanceTarget(&t); err != nil {
		return nil, err
	}
	if err := s.assertNameFree(t.Name, 0); err != nil {
		return nil, err
	}

	if err := s.db.Create(&t).Error; err != nil {
		return nil, fmt.Errorf("创建监控点失败: %w", err)
	}

	s.mu.RLock()
	view := s.viewOf(&t)
	s.mu.RUnlock()

	logger.Info("[balance] 已创建监控点", "id", t.ID, "name", t.Name, "type", t.Type)
	return &view, nil
}

// UpdateTarget 更新监控点。省略的字段保持原值。
func (s *BalanceService) UpdateTarget(id uint64, in BalanceTargetInput) (*BalanceTargetView, error) {
	t, err := s.GetTarget(id)
	if err != nil {
		return nil, err
	}

	if err := applyBalanceInput(t, in, false); err != nil {
		return nil, err
	}
	if err := validateBalanceTarget(t); err != nil {
		return nil, err
	}
	if err := s.assertNameFree(t.Name, id); err != nil {
		return nil, err
	}

	// 显式列更新而不是 Save：Save 会连 CreatedAt 和软删除列一起写回，
	// 把这条记录建库时的旧快照覆盖到可能已被并发改动的行上。
	if err := s.db.Model(&model.BalanceTarget{}).Where("id = ?", id).Updates(map[string]any{
		"name":       t.Name,
		"type":       t.Type,
		"base_url":   t.BaseURL,
		"api_key":    t.APIKey,
		"enabled":    t.Enabled,
		"sort_order": t.SortOrder,
	}).Error; err != nil {
		return nil, fmt.Errorf("更新监控点失败: %w", err)
	}

	s.mu.Lock()
	// 名称/地址可能变了，旧结果立刻失效——继续展示只会让人以为
	// 改完名称就拿到了新地址的数据。
	if entry, ok := s.results[id]; ok {
		entry.Name = t.Name
		entry.Type = t.Type
		entry.BaseURL = t.BaseURL
		entry.Enabled = t.Enabled
		entry.Status = "idle"
		entry.LastError = ""
	}
	s.mu.Unlock()

	refreshed, err := s.GetTarget(id)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	view := s.viewOf(refreshed)
	s.mu.RUnlock()

	logger.Info("[balance] 已更新监控点", "id", id, "name", t.Name)
	return &view, nil
}

// DeleteTarget 删除监控点并清掉它的缓存结果。
func (s *BalanceService) DeleteTarget(id uint64) error {
	t, err := s.GetTarget(id)
	if err != nil {
		return err
	}

	// 硬删除：这是一条纯配置记录，留软删除只会让唯一性校验
	// 与「同名重建」纠缠在一起。
	if err := s.db.Unscoped().Delete(&model.BalanceTarget{}, id).Error; err != nil {
		return fmt.Errorf("删除监控点失败: %w", err)
	}

	s.mu.Lock()
	delete(s.results, id)
	s.mu.Unlock()

	logger.Info("[balance] 已删除监控点", "id", id, "name", t.Name)
	return nil
}

// RefreshOne 立即查询单个监控点并返回结果。
func (s *BalanceService) RefreshOne(id uint64) (*BalanceTargetView, error) {
	t, err := s.GetTarget(id)
	if err != nil {
		return nil, err
	}
	if !t.Enabled {
		return nil, fmt.Errorf("%w: 监控点已停用，先启用再查询", ErrBalanceMissingField)
	}

	s.fetchOne(t)

	refreshed, err := s.GetTarget(id)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	view := s.viewOf(refreshed)
	s.mu.RUnlock()
	return &view, nil
}

// validateBalanceTarget 校验必填与类型，并顺手规范化。
func validateBalanceTarget(t *model.BalanceTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	t.BaseURL = strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
	t.Type = strings.ToLower(strings.TrimSpace(t.Type))

	if t.Name == "" {
		return fmt.Errorf("%w: 名称不能为空", ErrBalanceMissingField)
	}
	if t.BaseURL == "" {
		return fmt.Errorf("%w: base_url 不能为空", ErrBalanceMissingField)
	}
	if !strings.HasPrefix(t.BaseURL, "http://") && !strings.HasPrefix(t.BaseURL, "https://") {
		return fmt.Errorf("%w: base_url 必须是完整的 http(s) 地址", ErrBalanceMissingField)
	}
	if !model.IsValidBalanceTargetType(t.Type) {
		return fmt.Errorf("%w: %q（支持 %s）", ErrBalanceInvalidType, t.Type, strings.Join(model.BalanceTargetTypes(), "、"))
	}
	return nil
}

// assertNameFree 保证名称唯一（排除自身）。
//
// 名称是使用者用来辨认的标签，重名会让「DeepSeek 主号」出现两次，
// 缓存也分不清谁是谁。不看软删除列：删掉的名字应当可以重新用。
func (s *BalanceService) assertNameFree(name string, excludeID uint64) error {
	var count int64
	q := s.db.Unscoped().Model(&model.BalanceTarget{}).Where("name = ?", name)
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}
	if err := q.Count(&count).Error; err != nil {
		return fmt.Errorf("校验名称失败: %w", err)
	}
	if count > 0 {
		return ErrBalanceTargetExists
	}
	return nil
}

// applyBalanceInput 把请求体合并进模型。
//
// create 为真时执行额外处理：新建必须有 api_key（没有密钥的监控点
// 查询必然失败，让它建出来只是给用户埋一个红色状态），并补上默认值。
func applyBalanceInput(t *model.BalanceTarget, in BalanceTargetInput, create bool) error {
	if in.Name != nil {
		t.Name = *in.Name
	}
	if in.Type != nil {
		t.Type = *in.Type
	} else if create {
		t.Type = "deepseek"
	}
	if in.BaseURL != nil {
		t.BaseURL = *in.BaseURL
	} else if create {
		t.BaseURL = defaultBaseURLFor(t.Type)
	}
	if in.APIKey != nil {
		// 显式空串表示「清空密钥」，nil 表示「别动」。
		t.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	if in.SortOrder != nil {
		t.SortOrder = *in.SortOrder
	}

	if create && t.APIKey == "" {
		return fmt.Errorf("%w: api_key 不能为空", ErrBalanceMissingField)
	}
	return nil
}

// defaultBaseURLFor 给出供应商的默认地址，省掉新建时的一次查文档。
func defaultBaseURLFor(kind string) string {
	switch strings.ToLower(kind) {
	case "deepseek":
		return "https://api.deepseek.com"
	default:
		return ""
	}
}

// viewOf 把模型与缓存结果合成对外视图。调用方需持有读锁。
func (s *BalanceService) viewOf(t *model.BalanceTarget) BalanceTargetView {
	v := BalanceTargetView{
		ID:           t.ID,
		Name:         t.Name,
		Type:         t.Type,
		BaseURL:      t.BaseURL,
		Enabled:      t.Enabled,
		SortOrder:    t.SortOrder,
		HasAPIKey:    t.APIKey != "",
		APIKeyMasked: model.MaskAPIKey(t.APIKey),
		Status:       "idle",
	}
	if r, ok := s.results[t.ID]; ok {
		v.Balance = r.Balance
		v.Currency = r.Currency
		v.Status = r.Status
		v.LastError = r.LastError
		v.LastCheckedAt = r.LastCheckedAt
	}
	return v
}

// ============================================================
// 后台刷新
// ============================================================

// StartBackgroundRefresh 启动后台定时刷新。
//
// ctx 用于生命周期控制（如程序退出时取消）。
// 刷新间隔由 config.json 中的 balance_monitor.refresh_interval_seconds 控制。
// goroutine 登记在 wg 上，供关闭时等待它退出。
func (s *BalanceService) StartBackgroundRefresh(ctx context.Context, wg *sync.WaitGroup) {
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

	wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
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
	})
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
//
// 总数以数据库为准，而不是只看缓存：刚加进来还没刷过的监控点也该
// 计入总数，否则界面上会显示「1/1 可用」而实际有 3 个。
func (s *BalanceService) GetStatus() BalanceStatus {
	status := BalanceStatus{}

	var enabled int64
	if s.db != nil {
		s.db.Model(&model.BalanceTarget{}).Where("enabled = ?", true).Count(&enabled)
	}

	s.mu.RLock()
	status.LastRefresh = s.lastRefresh
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
	s.mu.RUnlock()

	// 缓存里还没有的启用项（刚建、还没刷）也要计入，否则进度看起来是错的。
	if pending := int(enabled) - status.TargetCount; pending > 0 {
		status.TargetCount += pending
		status.ErrorCount += pending
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

// loadEnabledTargets 从数据库读出启用的监控点。
func (s *BalanceService) loadEnabledTargets() ([]model.BalanceTarget, error) {
	var rows []model.BalanceTarget
	if err := s.db.Where("enabled = ?", true).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询监控点失败: %w", err)
	}
	return rows, nil
}

func (s *BalanceService) refreshAll() {
	targets, err := s.loadEnabledTargets()
	if err != nil {
		logger.Warn("[balance] 读取监控点失败", "error", err)
		return
	}
	if len(targets) == 0 {
		s.mu.Lock()
		s.lastRefresh = time.Now()
		s.mu.Unlock()
		return
	}

	// 先填充/更新缓存条目（基于数据库），标记为 checking
	s.mu.Lock()
	for i := range targets {
		t := &targets[i]
		entry, exists := s.results[t.ID]
		if !exists {
			entry = &TargetResult{ID: t.ID}
			s.results[t.ID] = entry
		}
		entry.Name = t.Name
		entry.Type = t.Type
		entry.BaseURL = t.BaseURL
		entry.Enabled = t.Enabled
		entry.Status = "checking"
	}
	s.mu.Unlock()

	for i := range targets {
		s.fetchOne(&targets[i])
	}

	s.mu.Lock()
	s.lastRefresh = time.Now()
	s.mu.Unlock()
}

func (s *BalanceService) fetchOne(t *model.BalanceTarget) {
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

	entry, exists := s.results[t.ID]
	if !exists {
		entry = &TargetResult{ID: t.ID, Name: t.Name, Type: t.Type, BaseURL: t.BaseURL, Enabled: t.Enabled}
		s.results[t.ID] = entry
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

func (s *BalanceService) fetchDeepSeek(t *model.BalanceTarget) (float64, string, error) {
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
		// 响应体不进错误信息：错误会回给前端并写进 last_error，而对方
		// 返回什么完全不受控（可能是内网服务的页面）。截断后只留 debug 日志。
		logger.Debug("[balance] 非 200 响应", "name", t.Name, "status", resp.StatusCode,
			"body", model.TruncateEllipsis(string(body), balanceErrorBodyLogLimit))
		return 0, "", fmt.Errorf("HTTP %d", resp.StatusCode)
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
