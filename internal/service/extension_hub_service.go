package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/hubclient"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
)

// ErrExtensionHubDisabled 表示对接没启用，或者配置不齐。
//
// Handler 用它区分「用户没开这个功能」与「开了但连不上」：
// 前者该收起入口，后者该显示错误。
var ErrExtensionHubDisabled = errors.New("未启用 Extension 对接")

// ErrExtensionHubInvalidInput 表示调用方给的参数不合法。
//
// 这类错误应当回 400，而不是当成上游故障。
var ErrExtensionHubInvalidInput = errors.New("参数不合法")

// ErrExtensionHubStorage 表示本地数据库读写失败。
//
// 必须与上游错误分开：本地查询挂了却报「上游不可达」，会把人送到
// 另一台机器上查一个根本没问题的服务。
var ErrExtensionHubStorage = errors.New("本地数据库操作失败")

// ExtensionHubService 把 hubclient 接到业务层。
//
// 它对齐两份订阅列表：本地 `rss_feeds`（权威来源）与 Extension 侧的
// 订阅（执行者）。推送是单向的 HomeHub → Extension，本地删除不会
// 自动传导过去——那需要用户显式确认。
//
// 客户端按配置懒构造。配置热更新后 base_url、私钥路径或超时任一变化，
// 下一次调用重建客户端；私钥文件内容只在路径变化时重读，与 Extension 侧
// 「公钥在进程启动时读一次」对称，避免热重载期间悄悄换了身份。
type ExtensionHubService struct {
	db *gorm.DB

	// cfg 返回当前生效的对接配置。默认读全局配置，测试可替换为固定值。
	// 每次调用都重新读：配置热重载后无需重启，也不需要通知回调。
	cfg func() config.ExtensionHubConfig

	// readyClient 用于 /readyz 探测。它不走签名通道，所以不复用 hubclient。
	// 超时给得短：这只是一条附加信息，不该拖慢状态接口。
	readyClient *http.Client

	mu     sync.Mutex
	cached *extensionHubClient
}

// extensionHubClient 缓存一次构造结果及其配置快照。
type extensionHubClient struct {
	baseURL     string
	keyPath     string
	timeoutSecs int
	// keyModTime / keySize 记录私钥文件的“版本”。
	//
	// 只比路径不够：轮换最常见的做法是 openssl genpkey -out 覆盖同名文件，
	// 路径不变而内容变了。只看路径就会一直用旧身份签出 401，且没有任何
	// 报错提示该重启——比直接失败更难排查。
	keyModTime time.Time
	keySize    int64

	client *hubclient.Client
	signer *hubclient.Signer
}

// NewExtensionHubService 创建对接服务。
func NewExtensionHubService(db *gorm.DB) *ExtensionHubService {
	return &ExtensionHubService{
		db:          db,
		readyClient: &http.Client{Timeout: 5 * time.Second},
		cfg: func() config.ExtensionHubConfig {
			if c := config.Get(); c != nil {
				return c.ExtensionHub
			}
			return config.ExtensionHubConfig{}
		},
	}
}

// client 返回当前配置对应的客户端与签名器。
//
// 每次调用都重新读一次配置：热重载之后不需要重启服务，也不需要
// 通知回调。代价是一次 atomic 读取和几次字符串比较。
func (s *ExtensionHubService) client() (*hubclient.Client, *hubclient.Signer, error) {
	hub := s.cfg()
	if !hub.Enabled {
		return nil, nil, ErrExtensionHubDisabled
	}
	if strings.TrimSpace(hub.BaseURL) == "" || strings.TrimSpace(hub.PrivateKeyPath) == "" {
		// 不包 ErrExtensionHubDisabled：配置项写漏了与用户根本没启用是
		// 两件事，前端要分别提示“去填配置”与“去打开开关”。
		return nil, nil, fmt.Errorf("base_url 与 private_key_path 都必须填写")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 先 stat 一次：它决定能否复用缓存，同时避开「路径没变但文件被覆盖」的坑。
	// stat 失败时 modTime/size 为零值，自然导向重建，重建时会把真实错误报出来。
	var keyModTime time.Time
	var keySize int64
	if info, statErr := os.Stat(hub.PrivateKeyPath); statErr == nil {
		keyModTime = info.ModTime()
		keySize = info.Size()
	}

	if c := s.cached; c != nil &&
		c.baseURL == hub.BaseURL &&
		c.keyPath == hub.PrivateKeyPath &&
		c.timeoutSecs == hub.TimeoutSeconds &&
		c.keyModTime.Equal(keyModTime) &&
		c.keySize == keySize {
		return c.client, c.signer, nil
	}

	signer, err := hubclient.LoadSigner(hub.PrivateKeyPath)
	if err != nil {
		// 不缓存失败：把私钥文件放到位后，下一次调用就能生效，不必重启。
		return nil, nil, err
	}
	client, err := hubclient.New(hub.BaseURL, signer)
	if err != nil {
		return nil, nil, err
	}
	if hub.TimeoutSeconds > 0 {
		client.Timeout = time.Duration(hub.TimeoutSeconds) * time.Second
	}

	s.cached = &extensionHubClient{
		baseURL:     hub.BaseURL,
		keyPath:     hub.PrivateKeyPath,
		timeoutSecs: hub.TimeoutSeconds,
		keyModTime:  keyModTime,
		keySize:     keySize,
		client:      client,
		signer:      signer,
	}
	logger.Info("Extension hub 通道已就绪",
		"base_url", hub.BaseURL,
		"public_key", signer.PublicKeyBase64(),
	)
	return client, signer, nil
}

// ============================================================
// 状态
// ============================================================

// ExtensionHubStatus 描述当前对接状态。
//
// 分三档回答，因为排障时它们是三个独立的问题：
//
//	Enabled / Configured   配置层面，不发网络请求就能回答
//	Reachable              网络层面：TCP + HTTP 通了
//	Authenticated          签名层面：Extension 认了这把私钥
//	RSSAvailable           模块层面：对方的 RSS 开关是否打开
//
// 不让前端自己推导，是因为状态码有重叠语义：404 既可能是「订阅不存在」
// 也可能是「RSS 模块未启用」；401 既可能是签名错也可能是时钟偏了。
// 本地推导必然猜错，所以在这里一次性判完。
type ExtensionHubStatus struct {
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	BaseURL    string `json:"base_url,omitempty"`
	// PublicKey 是宿主公钥（标准 base64），供部署时与 Extension 侧核对。
	// 它本来就是公开信息，不构成泄露。
	PublicKey string `json:"public_key,omitempty"`

	Reachable     bool `json:"reachable"`
	Authenticated bool `json:"authenticated"`
	RSSAvailable  bool `json:"rss_available"`
	// SubscriptionCount 只在 RSSAvailable 为真时有意义。
	SubscriptionCount int `json:"subscription_count"`

	// Ready 来自 Extension 的 /readyz（不签名、不进鉴权的那条探针）。
	//
	// 指针三态：nil 表示没探到（路径不对、端点不存在、超时）。
	// 与 Authenticated 分开是因为它们是两个问题：签名通过只说明
	// 「请求是真的」，不代表对端的数据库可读、blob 目录可写。
	Ready       *bool  `json:"ready"`
	ReadyDetail string `json:"ready_detail,omitempty"`

	// ErrorCode 优先取协议错误码（invalid_request / unauthenticated / …）；
	// 配置与网络层的问题用本包自定的 disabled / config / network。
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// Status 探测对接状态。会发起一次只读请求。
func (s *ExtensionHubService) Status(ctx context.Context) ExtensionHubStatus {
	var st ExtensionHubStatus

	// Enabled 先按配置填，不能等到 client() 成功才置真：
	// 用户明明写了 enabled=true 但私钥路径打错时，若报“未启用”，
	// 他会去翻 enabled 开关，而不是去查键文件。
	hub := s.cfg()
	st.Enabled = hub.Enabled
	st.BaseURL = hub.BaseURL
	if !hub.Enabled {
		st.ErrorCode = "disabled"
		st.ErrorMessage = ErrExtensionHubDisabled.Error()
		return st
	}

	client, signer, err := s.client()
	if err != nil {
		st.ErrorCode = "config"
		st.ErrorMessage = err.Error()
		return st
	}

	st.Configured = true
	if signer != nil {
		st.PublicKey = signer.PublicKeyBase64()
	}

	// 第一步用 /hub/keys 验证签名，而不是 /hub/subscriptions。
	//
	// 协议保证 /hub/keys **不受** rss.enabled 影响，所以它的 404 只可能是
	// 「base_url 路径写错」；而 /hub/subscriptions 的 404 同时可以是
	// 「RSS 模块未启用」。先用手不持有的 keys 端点验签，两条 404 才分得开：
	// 否则一个写错的 base_url 会被误报成「对方关了 RSS」。
	if _, err := client.ListKeys(ctx); err != nil {
		st.ErrorCode, st.ErrorMessage = classifyHubError(err)

		var apiErr *hubclient.APIError
		if errors.As(err, &apiErr) {
			st.Reachable = true
		}
		// 401 = 签名没过；这一支必须保持 Authenticated=false。
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// keys 端点不该返回 404：路径不对（或反向代理把 /api/v1 截掉了）。
			// 此时连通性成立、签名未知，但绝不能归因为「模块关闭」。
			st.ErrorCode = "config"
			st.ErrorMessage = fmt.Sprintf("base_url 下的 /hub/keys 返回 404，检查扩展地址是否包含 /api/v1 前缀（%s）", err)
		}
		return st
	}

	st.Reachable = true
	st.Authenticated = true

	subs, err := client.ListSubscriptions(ctx)
	if err != nil {
		st.ErrorCode, st.ErrorMessage = classifyHubError(err)
		// 签名已在上一步确认，这里的 404 就只可能是「RSS 模块未启用」了。
		return st
	}

	st.RSSAvailable = true
	st.SubscriptionCount = len(subs)

	// 最后再探一次 /readyz。它不影响前面的结论：拿不到就保持 nil，
	// 而不是判成「未就绪」——那会把一个没部署的探针演成故障。
	st.Ready, st.ReadyDetail = s.probeReady(ctx, hub.BaseURL)
	return st
}

// probeReady 请求 Extension 的 /readyz。
//
// base_url 形如 https://ext.example.com/api/v1，而 /readyz 挂在根上，
// 所以要先把版本前缀去掉。该端点按 Extension 的约定不签名、不记日志，
// 因此这里用裸 HTTP 请求，不走 hubclient。
//
// 整个过程是一个**尽力而为**的附加信息：超时、连不上、返回体不可解析
// 都只是让 Ready 留在 nil，绝不影响已经得出的可达性与签名结论。
func (s *ExtensionHubService) probeReady(ctx context.Context, baseURL string) (*bool, string) {
	root, ok := apiRoot(baseURL)
	if !ok {
		return nil, ""
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, root+"/readyz", nil)
	if err != nil {
		return nil, ""
	}

	resp, err := s.readyClient.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()

	// 404 表示对端没有这个端点（旧版本、或被反向代理挡了）。
	// 这时候「没探测到」比「未就绪」准确。
	if resp.StatusCode == http.StatusNotFound {
		return nil, ""
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	detail := strings.TrimSpace(string(body))
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}

	ready := resp.StatusCode == http.StatusOK
	return &ready, detail
}

// apiRoot 把 API 根地址还原成站点根。
//
// https://ext.example.com/api/v1 → https://ext.example.com
// 只有以 /api/v1 结尾时才能可靠地去掉：其他形式保留原样不可猜测。
func apiRoot(baseURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api/v1")
	u.RawPath = ""
	u.RawQuery = ""
	return strings.TrimSuffix(u.String(), "/"), true
}

// classifyHubError 把错误压成 (错误码, 说明) 给前端按码分支。
//
// 协议错误码原样透传；配置与传输层的问题用本包自定的三个值，
// 因为它们不属于协议，混进协议码会让人以为是 Extension 返回的。
func classifyHubError(err error) (code, message string) {
	if err == nil {
		return "", ""
	}
	var apiErr *hubclient.APIError
	if errors.As(err, &apiErr) {
		if apiErr.RequestID != "" {
			return apiErr.Code, fmt.Sprintf("%s（request_id=%s）", apiErr.Message, apiErr.RequestID)
		}
		return apiErr.Code, apiErr.Message
	}
	if errors.Is(err, ErrExtensionHubDisabled) {
		return "disabled", err.Error()
	}
	return "network", err.Error()
}

// ============================================================
// 订阅镜像
// ============================================================

// ExtensionHubPushResult 是推送单条订阅的结果。
type ExtensionHubPushResult struct {
	FeedID uint64 `json:"feed_id"`
	// RemoteID 是 Extension 侧的订阅 id，与本地 feed id 无关。
	RemoteID uint64                  `json:"remote_id"`
	Created  bool                    `json:"created"`
	Remote   *hubclient.Subscription `json:"remote"`
}

// PushFeed 把一条本地订阅推送到 Extension。
//
// enabled 为 nil 表示不碰对方的启停状态。这是对账与手动推送的默认值：
// 用户在 Extension 侧手动暂停过的源，不该被一次例行同步重新打开。
// 只有显式传 true 才表达「恢复」，此时 Extension 会一并清零失败计数。
func (s *ExtensionHubService) PushFeed(ctx context.Context, feedID uint64, enabled *bool, fetchNow bool) (*ExtensionHubPushResult, error) {
	client, _, err := s.client()
	if err != nil {
		return nil, err
	}

	var feed model.RSSFeed
	if err := s.db.First(&feed, feedID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRSSFeedNotFound
		}
		return nil, fmt.Errorf("%w: 查询订阅源失败: %v", ErrExtensionHubStorage, err)
	}

	remote, created, err := client.UpsertSubscription(ctx, feedToHubUpsert(&feed, enabled, fetchNow))
	if err != nil {
		return nil, err
	}
	return &ExtensionHubPushResult{
		FeedID:   feed.ID,
		RemoteID: remote.ID,
		Created:  created,
		Remote:   remote,
	}, nil
}

// ExtensionHubSyncItem 是单条订阅的同步结果。
type ExtensionHubSyncItem struct {
	FeedID   uint64 `json:"feed_id"`
	URL      string `json:"url"`
	Status   string `json:"status"` // created / updated / failed
	RemoteID uint64 `json:"remote_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ExtensionHubSyncResult 是全量对账的结果。
type ExtensionHubSyncResult struct {
	Total   int                    `json:"total"`
	Created int                    `json:"created"`
	Updated int                    `json:"updated"`
	Failed  int                    `json:"failed"`
	Items   []ExtensionHubSyncItem `json:"items"`
}

// SyncAll 把本地全部订阅补推到 Extension。
//
// 只推不删。删除会连带清掉 Extension 侧已抓取的条目与 blob，不可恢复，
// 必须由用户逐条确认——混在一次「对账」里悄悄删掉是最不该发生的事。
//
// 串行推送：Extension 是单写连接，并发只会互相排队并撞上 429 限流。
// 对账是低频操作，慢一点比报错好。也不触发抓取（fetch_now=false），
// 否则一次对账会对 N 个源同时发起抓取。
func (s *ExtensionHubService) SyncAll(ctx context.Context) (*ExtensionHubSyncResult, error) {
	client, _, err := s.client()
	if err != nil {
		return nil, err
	}

	var feeds []model.RSSFeed
	if err := s.db.Order("id").Find(&feeds).Error; err != nil {
		return nil, fmt.Errorf("%w: 查询订阅源失败: %v", ErrExtensionHubStorage, err)
	}

	result := &ExtensionHubSyncResult{
		Total: len(feeds),
		Items: make([]ExtensionHubSyncItem, 0, len(feeds)),
	}
	for i := range feeds {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		feed := &feeds[i]
		item := ExtensionHubSyncItem{FeedID: feed.ID, URL: feed.URL}

		remote, created, perr := client.UpsertSubscription(ctx, feedToHubUpsert(feed, nil, false))
		switch {
		case perr != nil:
			item.Status = "failed"
			item.Error = perr.Error()
			result.Failed++
		case created:
			item.Status = "created"
			item.RemoteID = remote.ID
			result.Created++
		default:
			item.Status = "updated"
			item.RemoteID = remote.ID
			result.Updated++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// ExtensionHubRemoteSubscription 是 Extension 侧订阅加上本地匹配信息。
//
// InLocal 用来回答「这条在 HomeHub 里还有没有」——正是对账要看的那个问题。
// 只用 URL 匹配：Extension 以 URL 为幂等键，id 体系与本地无关。
type ExtensionHubRemoteSubscription struct {
	hubclient.Subscription
	LocalFeedID uint64 `json:"local_feed_id,omitempty"`
	InLocal     bool   `json:"in_local"`
}

// RemoteSubscriptions 列出 Extension 侧的订阅，并标注本地是否也有。
func (s *ExtensionHubService) RemoteSubscriptions(ctx context.Context) ([]ExtensionHubRemoteSubscription, error) {
	client, _, err := s.client()
	if err != nil {
		return nil, err
	}

	remote, err := client.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	// 只取两列：这里要的是 URL → id 的映射，整行读出来没有意义。
	var feeds []model.RSSFeed
	if err := s.db.Select("id", "url").Find(&feeds).Error; err != nil {
		return nil, fmt.Errorf("%w: 查询订阅源失败: %v", ErrExtensionHubStorage, err)
	}
	byURL := make(map[string]uint64, len(feeds))
	for _, f := range feeds {
		byURL[f.URL] = f.ID
	}

	out := make([]ExtensionHubRemoteSubscription, 0, len(remote))
	for _, sub := range remote {
		item := ExtensionHubRemoteSubscription{Subscription: sub}
		if id, ok := byURL[sub.URL]; ok {
			item.LocalFeedID = id
			item.InLocal = true
		}
		out = append(out, item)
	}
	return out, nil
}

// DeleteRemoteSubscription 删除 Extension 侧的订阅。
//
// 返回 alreadyDeleted=true 表示对方本来就没有这条，宿主按「已删除」处理即可。
//
// 协议把「订阅不存在」与「RSS 模块未启用」都映射成 404，所以收到 404 后
// 还要再探测一次：列表端点也 404 就说明模块关着，那是配置问题，不能
// 当成删除成功——否则用户会以为删掉了，实际什么都没发生。
//
// 探测本身也可能失败（429 限流是协议的正常回答，5xx 与连接中断同理）。
// 那种情况下**不能**报成功：删除不可逆，把「没确认」说成「已经没了」
// 比直接报错严重得多——用户不会再去重试一条他以为已删掉的订阅。
func (s *ExtensionHubService) DeleteRemoteSubscription(ctx context.Context, id uint64) (alreadyDeleted bool, err error) {
	client, _, cerr := s.client()
	if cerr != nil {
		return false, cerr
	}

	err = client.DeleteSubscription(ctx, id)
	switch {
	case err == nil:
		return false, nil
	case !hubclient.IsNotFound(err):
		return false, err
	}

	// 到这里：DELETE 回了 404。必须再确认一次是「没有这条」还是「模块关着」。
	if _, lerr := client.ListSubscriptions(ctx); lerr != nil {
		if hubclient.IsNotFound(lerr) {
			return false, err // 模块关闭：把原本的 404 原样上报
		}
		return false, fmt.Errorf("%w（探测订阅列表失败，无法确认是否已删除: %v）", err, lerr)
	}
	return true, nil
}

// ============================================================
// 访问密钥
// ============================================================

// ListKeys 列出已签发的访问密钥。响应不含明文，也不含哈希。
func (s *ExtensionHubService) ListKeys(ctx context.Context) ([]hubclient.AccessKey, error) {
	client, _, err := s.client()
	if err != nil {
		return nil, err
	}
	return client.ListKeys(ctx)
}

// IssueKey 签发一把访问密钥。
//
// 返回值里的 Key 是明文，只出现这一次：Extension 存的是 sha256，
// 之后任何接口都拿不回。这里不做缓存、不写日志、不入库——一旦落盘，
// 「只出现一次」的保证就破了。
func (s *ExtensionHubService) IssueKey(ctx context.Context, name string, expiresIn *string) (*hubclient.CreatedKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		// binding:"required" 放行纯空白字符串，所以这里必须再拦一次：
		// 否则空格会被当成合法名称发到上游，得到的 400 反而指向 Extension。
		return nil, fmt.Errorf("%w：密钥名称不能为空", ErrExtensionHubInvalidInput)
	}

	client, _, err := s.client()
	if err != nil {
		return nil, err
	}

	key, err := client.CreateKey(ctx, hubclient.CreateKeyRequest{Name: name, ExpiresIn: expiresIn})
	if err != nil {
		return nil, err
	}

	// 只记 id 与名称，绝不记 Key 本身。
	logger.Info("已签发 Extension 访问密钥", "key_id", key.ID, "name", key.Name, "live", key.Live)
	return key, nil
}

// RevokeKey 撤销一把访问密钥，连带失效它换出去的会话。
func (s *ExtensionHubService) RevokeKey(ctx context.Context, id string) (alreadyDeleted bool, err error) {
	client, _, cerr := s.client()
	if cerr != nil {
		return false, cerr
	}

	err = client.RevokeKey(ctx, id)
	switch {
	case err == nil:
		logger.Info("已撤销 Extension 访问密钥", "key_id", id)
		return false, nil
	case !hubclient.IsNotFound(err):
		return false, err
	}
	return true, nil
}

// ============================================================
// 字段映射
// ============================================================

// feedToHubUpsert 把本地订阅源映射成推送请求。
//
// 三处刻意的取舍：
//
//   - Title 为空时传 nil 而不是空串。Extension 用「省略」表达「保留已学到的
//     标题」，显式空串会把对方抓到的标题清掉——本地还没抓到标题时尤其明显。
//   - FetchInterval <= 0 时省略 interval，让对方用自己的默认值。推 0 会被
//     夹到下限，值虽然合法，语义却是错的。
//   - FetchNow 总是显式传。省略等于 true，那「不立即抓取」就表达不出来了。
func feedToHubUpsert(feed *model.RSSFeed, enabled *bool, fetchNow bool) hubclient.UpsertSubscriptionRequest {
	req := hubclient.UpsertSubscriptionRequest{
		URL:      feed.URL,
		Enabled:  enabled,
		FetchNow: &fetchNow,
	}
	if title := strings.TrimSpace(feed.Title); title != "" {
		req.Title = &title
	}
	if feed.FetchInterval > 0 {
		interval := formatHubInterval(feed.FetchInterval)
		req.Interval = &interval
	}
	return req
}

// formatHubInterval 把秒数转成 Extension 接受的紧凑时长写法。
//
// 不用 time.Duration.String()：它会给出 "1h0m0s" 这种尾巴，Go 能解析，
// 但与协议示例（"30m"、"1h30m"、"6h"）不一致，日志里也很难读。
//
// 不是整分钟的间隔原样用秒（如 90 → "90s"）而不凑成 "1m30s"：
// Extension 的夹紧范围是分钟级，秒级只会在配置写得很怪时出现，
// 保留原值更容易和配置对上账。
func formatHubInterval(seconds int) string {
	if seconds <= 0 {
		return ""
	}

	hours := seconds / 3600
	minutes := (seconds % 3600) / 60

	switch {
	case seconds%60 != 0:
		return fmt.Sprintf("%ds", seconds)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
