package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
// 下一次调用重建客户端。
//
// 私钥只有一处来源：config.json 的 extension.private_key_pem。
//
// 旧版把私钥放数据库（多实例共享）或单独文件（兜底），代价是「到底在
// 用哪一份」变成需要运行期查询的问题，还要额外维护一张表与一套迁移。
// config.json 本身已是最高敏感度的文件（0600、gitignore、备份必有），
// 再拆一处并不更安全，只是更绕。
type ExtensionHubService struct {
	db *gorm.DB

	// cfg 返回当前生效的连接配置。默认读全局配置，测试可替换为固定值。
	// 每次调用都重新读：配置热重载后无需重启，也不需要通知回调。
	cfg func() config.ExtensionConfig

	// readyClient 用于 /readyz 探测。它不走签名通道，所以不复用 hubclient。
	// 超时给得短：这只是一条附加信息，不该拖慢状态接口。
	readyClient *http.Client

	mu     sync.Mutex
	cached *extensionHubClient
}

// extensionHubClient 缓存一次构造结果及其配置快照。
type extensionHubClient struct {
	baseURL string
	// keyFingerprint 是私钥 PEM 的短哈希，用作判重依据。
	//
	// 只比 baseURL 不够：轮换密钥最常见的方式就是原地改配置里的 PEM，
	// 地址没变。不比内容就会一直用旧身份签出 401，且没有任何提示。
	keyFingerprint string
	timeoutSecs    int

	client *hubclient.Client
	signer *hubclient.Signer
}

// KeySourceConfig 是唯一可能的密钥来源，进日志与状态接口。
const KeySourceConfig = "config"

// NewExtensionHubService 创建对接服务。
func NewExtensionHubService(db *gorm.DB) *ExtensionHubService {
	return &ExtensionHubService{
		db:          db,
		readyClient: &http.Client{Timeout: 5 * time.Second},
		cfg: func() config.ExtensionConfig {
			if c := config.Get(); c != nil {
				return c.Extension
			}
			return config.ExtensionConfig{}
		},
	}
}

// client 返回当前配置对应的客户端与签名器。
//
// 每次调用都重新读一次配置：热重载之后不需要重启服务，也不需要
// 通知回调。代价是一次 atomic 读取与一次很轻的字符串比较。
func (s *ExtensionHubService) client() (*hubclient.Client, *hubclient.Signer, error) {
	ext := s.cfg()
	if !ext.Enabled || !ext.Features.Hub {
		return nil, nil, ErrExtensionHubDisabled
	}
	if strings.TrimSpace(ext.BaseURL) == "" {
		// 不包 ErrExtensionHubDisabled：配置项写漏了与用户根本没启用是
		// 两件事，前端要分别提示"去填配置"与"去打开开关"。
		return nil, nil, fmt.Errorf("extension.base_url 必须填写")
	}

	pemText := strings.TrimSpace(ext.PrivateKeyPEM)
	if pemText == "" {
		return nil, nil, fmt.Errorf("没有可用的签名私钥：extension.private_key_pem 未配置")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fingerprint := fingerprintKey(pemText)
	if c := s.cached; c != nil &&
		c.baseURL == ext.BaseURL &&
		c.timeoutSecs == ext.TimeoutSeconds &&
		c.keyFingerprint == fingerprint {
		return c.client, c.signer, nil
	}

	signer, err := hubclient.LoadSignerFromBytes([]byte(pemText))
	if err != nil {
		return nil, nil, fmt.Errorf("解析 extension.private_key_pem 失败: %w", err)
	}

	// 自签自验：能解析不等于能用。
	//
	// LoadSignerFromBytes 对「裸字节」只校验长度，因此任意 64 字节文本
	// 都会被当成合法私钥。粘贴时漏了一段 base64、或者把别的内容混进来，
	// 长度恰好是 64 就会静默通过，直到每个请求都 401 才发现。
	// 一次签名+验签是微秒级开销，只在重建客户端时发生。
	probe := []byte("homehub-client-selfcheck")
	if !ed25519.Verify(signer.PublicKey(), probe, signer.SignRaw(probe)) {
		return nil, nil, fmt.Errorf("extension.private_key_pem 自检失败，内容可能已损坏")
	}

	client, err := hubclient.New(ext.BaseURL, signer)
	if err != nil {
		return nil, nil, err
	}
	if ext.TimeoutSeconds > 0 {
		client.Timeout = time.Duration(ext.TimeoutSeconds) * time.Second
	}

	// Extension 默认直连：通常是内网或同机部署，绕代理反而更慢，
	// 而且很多代理不转发内网地址。需要时由 extension.proxy_names 指定。
	transport, terr := config.ExtensionTransport()
	if terr != nil {
		logger.Warn("[extension] 代理配置不可用，改为直连", "error", terr)
		transport, _ = config.TransportFor(nil)
	}
	client.SetHTTPClient(&http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})

	s.cached = &extensionHubClient{
		baseURL:        ext.BaseURL,
		keyFingerprint: fingerprint,
		timeoutSecs:    ext.TimeoutSeconds,
		client:         client,
		signer:         signer,
	}
	logger.Info("Extension 通道已就绪",
		"base_url", ext.BaseURL,
		"key_source", KeySourceConfig,
		"public_key", signer.PublicKeyBase64(),
	)
	return client, signer, nil
}

// invalidateCache 丢掉缓存的客户端，让下一次调用按新密钥重建。
func (s *ExtensionHubService) invalidateCache() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
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
	// PublicKeyPEM 是同一把公钥的 PKIX PEM 全文，部署时用它。
	PublicKeyPEM string `json:"public_key_pem,omitempty"`
	// KeySource 是这把密钥的来处：config / none。
	KeySource string `json:"key_source,omitempty"`
	// KeyFingerprint 是公钥的 SHA-256 前 16 位，用来在不暴露公钥全串的
	// 场合核对身份（例如日志、工单）。私钥永远不会出现在任何响应里。
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	Reachable     bool `json:"reachable"`
	Authenticated bool `json:"authenticated"`
	RSSAvailable  bool `json:"rss_available"`
	// SubscriptionCount 只在 RSSAvailable 为真时有意义。
	SubscriptionCount int `json:"subscription_count"`

	// Capabilities 记录对端是否真的实现了 /fetch 与 /avatar。
	//
	// 声明式配置（extension.features）只能表达「我想用」，不能证明
	// 「对端有」。开着一个对端没实现的开关时，表现是每次抓取/头像都失败，
	// 而错误看起来像上游故障。这里用一次无副作用的探测把答案摊开。
	//
	// 两条都探，不论 features 是否已开启：开关打开前就应当能看到
	// 「对端到底有没有」，否则只能靠打开后失败来发现。探测本身是
	// 无参数 GET，代价接近一个 RTT。
	FetchAvailable  *bool `json:"fetch_available"`
	AvatarAvailable *bool `json:"avatar_available"`

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
	if !hub.Enabled || !hub.Features.Hub {
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
		st.PublicKeyPEM, _ = signer.PublicKeyPEM()
		st.KeyFingerprint = fingerprintKey(st.PublicKey)
	}
	// 密钥来源要单独查一次：client() 只回 signer，不带来源标记。
	// 查失败时留空，不把它算成故障——状态的主结论已经拿到了。
	if info, err := s.KeyInfo(); err == nil {
		st.KeySource = info.Source
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

	// 能力探测。只探用户已开启的那几条，且失败一律留 nil。
	st.FetchAvailable = probeEndpoint(ctx, client, "/fetch")
	st.AvatarAvailable = probeEndpoint(ctx, client, "/avatar/00000000000000000000000000000000")

	// 最后再探一次 /readyz。它不影响前面的结论：拿不到就保持 nil，
	// 而不是判成「未就绪」——那会把一个没部署的探针演成故障。
	st.Ready, st.ReadyDetail = s.probeReady(ctx, hub.BaseURL)
	return st
}

// probeEndpoint 判断对端是否实现了某个端点。
//
// 判据是「不是 404、也不是 SPA 兜底的 HTML」：
//   - 404  → 端点不存在（或 RSS 模块关闭，但那条由 RSSAvailable 表达）
//   - 200 + HTML → 落到了 SPA 兜底，端点其实不存在。这是当前 Extension
//     的真实形态：/fetch 与 /avatar 都返回 630 字节的首页。
//   - 400/401/422 → 端点存在，只是我们没给对参数
//
// 请求本身没有副作用：/fetch 缺 url 参数、/avatar 用全零 hash，都不会
// 触发任何抓取或下载。失败一律返回 nil，不让它污染状态主结论。
func probeEndpoint(ctx context.Context, client *hubclient.Client, path string) *bool {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.DoRaw(probeCtx, http.MethodGet, path, "", "*/*", nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	yes, no := true, false
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return &no
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// 2xx 只可能是 SPA 兜底或真的端点。看 Content-Type：
		// feed 是 XML，头像是图片，都不该是 text/html。
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
			return &no
		}
		return &yes
	default:
		// 其余状态码（400/401/403/405/422…）说明路由存在，只是参数不对。
		return &yes
	}
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
	// Total 是本次推送到 Extension 的条数，不含归属本机的源。
	Total   int `json:"total"`
	Created int `json:"created"`
	Updated int `json:"updated"`
	Failed  int `json:"failed"`
	// SkippedLocal 是归属本机抓取、因此没有推送的条数。
	//
	// 单独报出来是因为「total=0」有两种含义：本地确实没有订阅源，
	// 或者有源但都归本机。不区分的话，使用者会以为对账没干活。
	SkippedLocal int                    `json:"skipped_local"`
	Items        []ExtensionHubSyncItem `json:"items"`
}

// SyncAll 把归属 Extension 的订阅补推到 Extension。
//
// 只推 fetch_by=extension 的源。全推的后果是镜像里混进一批本机也在抓
// 的源：Extension 新建时默认启用，于是同一篇文章两边各抓一份、各存一份，
// 而使用者只会在其中一处阅读。归属判断与 FetchAll 共用 NormalizeFetchBy，
// 两处必须同源——否则会出现「本机不抓、也没推给对方」的源，静默停摆。
//
// 只推不删。删除会连带清掉 Extension 侧已抓取的条目与 blob，不可恢复，
// 必须由用户逐条确认——混在一次「对账」里悄悄删掉是最不该发生的事。
// 由此带来一个已知不对等：把某个源从 extension 改回 local 之后，
// Extension 侧那条仍然存在且继续抓取，需要用户自己到 Extension 界面删。
//
// enabled 一律传 nil（不碰对方启停状态），理由见 PushFeed 的注释：
// 用户在 Extension 侧手动暂停过的源，不该被一次例行对账重新打开。
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

	// 分流在内存里做而不是写进 WHERE：
	//   1. 与 FetchAll 的 NormalizeFetchBy 判断保持逐字一致，
	//      空串与历史数据都按 local 处理；
	//   2. 同一次查询就能统计出 skipped，不必再数一次。
	pending := make([]model.RSSFeed, 0, len(feeds))
	skipped := 0
	for i := range feeds {
		if model.NormalizeFetchBy(feeds[i].FetchBy) != model.FetchByExtension {
			skipped++
			continue
		}
		pending = append(pending, feeds[i])
	}

	result := &ExtensionHubSyncResult{
		Total:        len(pending),
		SkippedLocal: skipped,
		Items:        make([]ExtensionHubSyncItem, 0, len(pending)),
	}
	for i := range pending {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		feed := &pending[i]
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
// 签名私钥（存数据库，供多实例共享）
// ============================================================

// ExtensionHubKeyInfo 是密钥的对外视图。**不含私钥**。
type ExtensionHubKeyInfo struct {
	// Source 只有两个取值：config（已配置）与 none（未配置）。
	Source string `json:"source"`
	// Configured 是 Source 的布尔形式，前端直接用这个。
	Configured bool   `json:"configured"`
	PublicKey  string `json:"public_key,omitempty"`
	// PublicKeyPEM 是 PKIX PEM 全文，部署时优先用它：
	// 自带类型标记，不会与「DER 的 base64」混淆。
	PublicKeyPEM string `json:"public_key_pem,omitempty"`
	// Fingerprint 是公钥的 SHA-256 前 16 位，便于在日志里核对。
	Fingerprint string `json:"fingerprint,omitempty"`
	// Broken 为真表示配置里的 PEM 存在但解析不了。
	// 与「未配置」分开：两者的修法不同（一个要重新粘贴，一个要先配）。
	Broken bool `json:"broken,omitempty"`
}

// fingerprintKey 取**裸公钥字节** SHA-256 的前 16 位（8 字节 hex）。
//
// 必须哈希裸字节而不是 base64 字符串：同一把公钥的 PEM 与 base64
// 两种写法字符串完全不同，若按字符串算就会得到两个指纹，而指纹的
// 全部用处就是「换个地方看到的一串是不是同一把」。
//
// 输入接受裸 32 字节 base64（库里存的格式）。解析不出来时退化为
// 对字符串取哈希：宁可给一个不完美的值，也不要让状态接口跟着失败。
func fingerprintKey(publicBase64 string) string {
	if raw, err := base64.StdEncoding.DecodeString(publicBase64); err == nil && len(raw) == ed25519.PublicKeySize {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:8])
	}
	sum := sha256.Sum256([]byte(publicBase64))
	return hex.EncodeToString(sum[:8])
}

// publicKeyPEMFromBase64 把裸 32 字节公钥的 base64 还原成 PKIX PEM。
//
// 库里只存 base64（省一半空间，且与 Status.PublicKey 同格式），
// 但部署时要的是 PEM。这里补 12 字节的 ASN.1 头再包一层 PEM：
// Ed25519 的 PKIX 头是固定的 302a300506032b6570032100。
//
// 变换失败时返回空串而不是报错：公钥只是展示用，缺它不影响
// 「密钥到底有没有」这个主结论。
func publicKeyPEMFromBase64(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return ""
	}
	der, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(raw))
	if err != nil {
		return ""
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return string(block)
}

// KeyInfo 描述当前生效的密钥，**不含私钥**。
//
// 三种情况要能区分清楚，否则用户不知道该改哪里：
//   - 已配置且能解析：正常，回公钥与指纹
//   - 未配置：extension.private_key_pem 为空
//   - 配了但解析不了：粘贴时截断了，或者混进了别的内容
func (s *ExtensionHubService) KeyInfo() (*ExtensionHubKeyInfo, error) {
	pemText := strings.TrimSpace(s.cfg().PrivateKeyPEM)
	if pemText == "" {
		return &ExtensionHubKeyInfo{Source: "none"}, nil
	}

	signer, err := hubclient.LoadSignerFromBytes([]byte(pemText))
	if err != nil {
		// 解析失败不返回错误：状态接口要能回答「现在配了一把坏的私钥」，
		// 直接报错会让前端连这个事实都拿不到。
		return &ExtensionHubKeyInfo{Source: KeySourceConfig, Broken: true}, nil
	}

	pub := signer.PublicKeyBase64()
	pemOut, _ := signer.PublicKeyPEM()
	return &ExtensionHubKeyInfo{
		Source:       KeySourceConfig,
		Configured:   true,
		PublicKey:    pub,
		PublicKeyPEM: pemOut,
		Fingerprint:  fingerprintKey(pub),
	}, nil
}

// SetKey 把私钥写进 config.json 的 extension.private_key_pem。
//
// 接受与 LoadSigner 相同的三种形态（PKCS#8 PEM / 裸种子 / base64 文本），
// 但落盘一律规范成 PEM：文件里存一份统一格式，回读时不用再猜。
//
// 校验比「能解析」严格一档：解析出来之后立刻自签一次并验签，
// 确认这把私钥真的可用。只解析不验证会放过「长度对但内容坏」的输入。
func (s *ExtensionHubService) SetKey(raw string) (*ExtensionHubKeyInfo, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%w: 私钥内容不能为空", ErrExtensionHubInvalidInput)
	}

	signer, err := hubclient.LoadSignerFromBytes([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: 无法解析私钥: %v", ErrExtensionHubInvalidInput, err)
	}

	// 自签自验：能解析不等于能用（例如被截断的 base64 可能解出错误长度）。
	probe := []byte("homehub-key-selfcheck")
	if !ed25519.Verify(signer.PublicKey(), probe, signer.SignRaw(probe)) {
		return nil, fmt.Errorf("%w: 私钥自检失败，内容可能已损坏", ErrExtensionHubInvalidInput)
	}

	// 落盘用一行 base64 种子而不是 PEM。
	//
	// PEM 是四行文本，进 JSON 要写成 \n 转义，粘进 config.json 后
	// 既难读也容易在复制过程中混入不可见字符。32 字节种子的 base64
	// 是 44 个字符，一行装得下，且足以还原完整私钥。
	//
	// 读取侧仍然兼容 PEM 与完整私钥的 base64：LoadSignerFromBytes
	// 三种形态都认，从 openssl 直接粘过来也能用。
	seed := signer.SeedBase64()

	if err := writeExtensionField("private_key_pem", seed); err != nil {
		return nil, fmt.Errorf("%w: 写入配置失败: %v", ErrExtensionHubStorage, err)
	}

	s.invalidateCache()
	logger.Info("已把签名私钥写入 config.json（base64 种子）",
		"public_key", signer.PublicKeyBase64(),
		"fingerprint", fingerprintKey(signer.PublicKeyBase64()))
	return s.KeyInfo()
}

// DeleteKey 清空 config.json 里的私钥。
//
// 返回 false 表示本来就没配。这里不阻止「删掉之后无密钥可用」——
// 那是用户的部署选择，拒绝反而更难办；但 enabled 仍为 true 时会
// 被配置校验拦下，需要先关掉开关，错误信息会说明这一点。
func (s *ExtensionHubService) DeleteKey() (bool, error) {
	if strings.TrimSpace(s.cfg().PrivateKeyPEM) == "" {
		return false, nil
	}

	if err := writeExtensionField("private_key_pem", nil); err != nil {
		return false, fmt.Errorf("%w: %v", ErrExtensionHubInvalidInput, err)
	}

	s.invalidateCache()
	logger.Info("已从 config.json 移除签名私钥")
	return true, nil
}

// writeExtensionField 改写 config.json 的 extension 段中的单个字段。
//
// value 为 nil 表示删除该键。用局部更新而不是整体覆盖：extension 段
// 里还有其他字段，整体写回会让「只改密钥」变成一次全量替换。
func writeExtensionField(key string, value any) error {
	return config.AtomicJSONUpdate(config.Path(), func(raw map[string]any) error {
		section, ok := raw["extension"].(map[string]any)
		if !ok {
			section = map[string]any{}
			raw["extension"] = section
		}
		if value == nil {
			delete(section, key)
			if enabled, _ := section["enabled"].(bool); enabled {
				return fmt.Errorf("extension.enabled 仍为 true，请先关闭后再清除私钥")
			}
			return nil
		}
		section[key] = value
		return nil
	})
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

// ============================================================
// 共用的出站客户端构造
// ============================================================

// errExtensionNotConfigured 表示没有可用的 Extension 连接配置。
//
// 与「配置了但坏了」分开：前者应当静默回落到本地实现（用户可能
// 根本没打算用 Extension），后者应当报错，避免把人引向错误的方向。
var errExtensionNotConfigured = errors.New("extension 未启用或未配置")

// buildExtensionClient 从配置构造一个带签名的 Extension 客户端。
//
// 供 RSS 代抓与头像代理使用：它们和 /hub/* 是同一个对端、同一把密钥，
// 认证方式必须一致。旧版这两条通路各用一套 Bearer token，结果是同一个
// 部署里存在两套凭据，换一次 token 要改三个地方。
func buildExtensionClient(cfg *config.Config, timeout time.Duration) (*hubclient.Client, error) {
	if cfg == nil || !cfg.Extension.Enabled || strings.TrimSpace(cfg.Extension.BaseURL) == "" {
		return nil, errExtensionNotConfigured
	}

	pemText := strings.TrimSpace(cfg.Extension.PrivateKeyPEM)
	if pemText == "" {
		return nil, fmt.Errorf("extension.private_key_pem 未配置")
	}

	signer, err := hubclient.LoadSignerFromBytes([]byte(pemText))
	if err != nil {
		return nil, fmt.Errorf("解析 extension.private_key_pem 失败: %w", err)
	}

	client, err := hubclient.New(cfg.Extension.BaseURL, signer)
	if err != nil {
		return nil, err
	}
	if timeout > 0 {
		client.Timeout = timeout
	}

	// Extension 默认直连，需要时代理由 extension.proxy_names 指定。
	transport, terr := config.ExtensionTransport()
	if terr != nil {
		logger.Warn("[extension] 代理配置不可用，改为直连", "error", terr)
		transport, _ = config.TransportFor(nil)
	}
	// 不跟随重定向：地址由运维配置，跳转只可能是配置错误或劫持，
	// 静默跟随会把签名头带给第三方。
	client.SetHTTPClient(&http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})

	return client, nil
}
