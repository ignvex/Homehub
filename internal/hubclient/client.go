package hubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 签名头，协议文档 §3.1 固定这三个名字。
const (
	headerTimestamp = "X-HH-Timestamp"
	headerNonce     = "X-HH-Nonce"
	headerSignature = "X-HH-Signature"
)

// 端点路径，相对于 API 根（形如 https://host/api/v1）。
const (
	pathSubscriptions = "/hub/subscriptions"
	pathKeys          = "/hub/keys"
)

// 默认值。刷新是同步执行的、最长 2 分钟，所以它用一个更宽的预算。
const (
	DefaultTimeout        = 30 * time.Second
	DefaultRefreshTimeout = 2*time.Minute + 15*time.Second
	DefaultUserAgent      = "homehub-hubclient/1.0"
	// maxResponseBytes 防止异常响应把内存吃满。列表接口返回的是元数据，
	// 8MB 远高于实际需要。
	maxResponseBytes = 8 << 20
)

// Client 是 Extension /hub/* 通道的客户端。
//
// 零值不可用，必须用 New 构造。字段可在首次调用前调整，调用开始后
// 视为只读 —— Client 本身并发安全。
type Client struct {
	base   *url.URL
	signer *Signer
	http   *http.Client

	// Timeout 是单次请求的预算，默认 30 秒。
	Timeout time.Duration
	// RefreshTimeout 覆盖 RefreshSubscription 的预算，因为它是同步抓取。
	RefreshTimeout time.Duration
	// UserAgent 用于区分调用来源，便于 Extension 侧排查。
	UserAgent string
}

// New 构造客户端。baseURL 是 API 根，需要带上版本前缀，例如
// https://example.com/api/v1 —— 端点会拼在其后。
//
// signer 不可为 nil：这条通道没有匿名模式。
func New(baseURL string, signer *Signer) (*Client, error) {
	if signer == nil {
		return nil, fmt.Errorf("hubclient: 必须提供签名器")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("hubclient: 解析 base URL 失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("hubclient: base URL 必须是 http(s) 地址，实际 %q", baseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("hubclient: base URL 缺少主机名: %q", baseURL)
	}
	return &Client{
		base:           u,
		signer:         signer,
		http:           &http.Client{},
		Timeout:        DefaultTimeout,
		RefreshTimeout: DefaultRefreshTimeout,
		UserAgent:      DefaultUserAgent,
	}, nil
}

// HTTPClient 允许注入自定义 *http.Client（例如带代理或连接的 Transport）。
func (c *Client) HTTPClient() *http.Client { return c.http }

// SetHTTPClient 替换底层 HTTP 客户端。传 nil 会恢复成默认值。
func (c *Client) SetHTTPClient(h *http.Client) {
	if h == nil {
		h = &http.Client{}
	}
	c.http = h
}

// do 构造、签名并发送一条请求。
//
// 返回 HTTP 状态码与错误；2xx 时若 out 非空则把响应体解析进去。
func (c *Client) do(ctx context.Context, method, path, rawQuery string, body []byte, out any) (int, error) {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	// 清掉基址可能带的 RawPath，让 Go 依据 Path 重新计算编码，
	// 保证"签名用的路径"与"实际发出的路径"是同一份字节。
	u.RawPath = ""
	u.RawQuery = rawQuery

	ts := Now().Unix()
	nonce, err := NewNonce()
	if err != nil {
		return 0, err
	}
	signature := c.signer.Sign(method, u.EscapedPath(), u.RawQuery, body, ts, nonce)

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, u.String(), reader)
	if err != nil {
		return 0, fmt.Errorf("hubclient: 构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(headerTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(headerNonce, nonce)
	req.Header.Set(headerSignature, signature)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("hubclient: 请求失败: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("hubclient: 读取响应失败: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil && len(bytes.TrimSpace(payload)) > 0 {
			if err := json.Unmarshal(payload, out); err != nil {
				return resp.StatusCode, fmt.Errorf("hubclient: 解析响应失败: %w", err)
			}
		}
		return resp.StatusCode, nil
	}

	return resp.StatusCode, newAPIError(resp, payload)
}

// DoRaw 发送一条签名请求并返回未经解析的响应。
//
// 供 `/fetch`、`/avatar` 这类返回 XML 或二进制的端点使用：do() 会在
// 2xx 时尝试 JSON 解码，对这两条通路没有意义。调用方负责关闭 Body。
//
// 非 2xx 不会自动转成 error，由调用方按自己的错误语义处理（头像要把
// 404 与 304 分开对待）。ctx 的超时由调用方控制。
func (c *Client) DoRaw(ctx context.Context, method, path, rawQuery, accept string, extra http.Header) (*http.Response, error) {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawPath = ""
	u.RawQuery = rawQuery

	ts := Now().Unix()
	nonce, err := NewNonce()
	if err != nil {
		return nil, err
	}
	signature := c.signer.Sign(method, u.EscapedPath(), u.RawQuery, nil, ts, nonce)

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)

	req, err := http.NewRequestWithContext(reqCtx, method, u.String(), nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("hubclient: 构造请求失败: %w", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	// 条件请求头（If-None-Match / If-Modified-Since）由调用方决定，
	// 这里只负责透传，不对内容作任何解释。
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	req.Header.Set(headerTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(headerNonce, nonce)
	req.Header.Set(headerSignature, signature)

	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("hubclient: 请求失败: %w", err)
	}
	// 取消要绑在 Body 关闭上：调用方读完才结束，否则会在传输中途关连接。
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose 在关闭响应体时释放关联的 context。
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// newAPIError 从响应构造结构化错误。响应体不是预期格式时退化为状态码映射。
func newAPIError(resp *http.Response, payload []byte) *APIError {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Code:       codeForStatus(resp.StatusCode),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}

	var envelope errorEnvelope
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Error.Code != "" {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
		apiErr.RequestID = envelope.Error.RequestID
		return apiErr
	}

	// 网关或代理返回的非协议错误：保留可读片段，避免只看到状态码。
	snippet := strings.TrimSpace(string(payload))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	apiErr.Message = snippet
	return apiErr
}

// codeForStatus 在响应缺少协议错误体时给出兜底错误码。
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidRequest
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadGateway:
		return CodeUpstreamError
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	default:
		return CodeInternal
	}
}

// UpsertSubscription 推送或更新订阅，返回结果与"是否新建"。
//
// Extension 用 URL 作为幂等键：同一条 URL 再次推送是更新（200），
// 首次推送是新建（201）。
func (c *Client) UpsertSubscription(ctx context.Context, in UpsertSubscriptionRequest) (*Subscription, bool, error) {
	if strings.TrimSpace(in.URL) == "" {
		return nil, false, fmt.Errorf("hubclient: url 必填")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, false, fmt.Errorf("hubclient: 序列化请求失败: %w", err)
	}

	var out Subscription
	status, err := c.do(ctx, http.MethodPost, pathSubscriptions, "", body, &out)
	if err != nil {
		return nil, false, err
	}
	return &out, status == http.StatusCreated, nil
}

// ListSubscriptions 对账用：取 Extension 侧的全部订阅。
//
// 没有订阅时返回空切片而不是 nil，调用方可以直接 range。
func (c *Client) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	var out subscriptionList
	if _, err := c.do(ctx, http.MethodGet, pathSubscriptions, "", nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		return []Subscription{}, nil
	}
	return out.Items, nil
}

// DeleteSubscription 移除订阅，连带清理条目与 blob 引用。
//
// 重复删除会得到 404 而不是 204，调用方按"已经删掉了"处理即可。
func (c *Client) DeleteSubscription(ctx context.Context, id uint64) error {
	_, err := c.do(ctx, http.MethodDelete, pathSubscriptions+"/"+strconv.FormatUint(id, 10), "", nil, nil)
	return err
}

// RefreshSubscription 同步触发一次抓取，最长 2 分钟。
//
// 目标站点失败会返回 502 upstream_error，订阅不存在返回 404；
// 两种情况都会更新 error_count 与 next_fetch_at。
func (c *Client) RefreshSubscription(ctx context.Context, id uint64) (*RefreshResult, error) {
	timeout := c.RefreshTimeout
	if timeout <= 0 {
		timeout = DefaultRefreshTimeout
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var out RefreshResult
	_, err := c.do(refreshCtx, http.MethodPost,
		pathSubscriptions+"/"+strconv.FormatUint(id, 10)+"/refresh", "", nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateKey 签发访问密钥。
//
// 返回值里的 Key 只出现这一次。调用方要当场交付或写入安全存储，
// 之后任何接口都拿不回明文。
func (c *Client) CreateKey(ctx context.Context, in CreateKeyRequest) (*CreatedKey, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("hubclient: name 必填")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("hubclient: 序列化请求失败: %w", err)
	}

	var out CreatedKey
	if _, err := c.do(ctx, http.MethodPost, pathKeys, "", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListKeys 列出已签发的访问密钥，响应不含明文也不含哈希。
//
// 对账只看 Live 字段：它由服务端计算，避免用本地时钟判断过期。
func (c *Client) ListKeys(ctx context.Context) ([]AccessKey, error) {
	var out keyList
	if _, err := c.do(ctx, http.MethodGet, pathKeys, "", nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		return []AccessKey{}, nil
	}
	return out.Items, nil
}

// RevokeKey 撤销访问密钥，同时失效其绑定客户端与全部 refresh 会话。
//
// 撤销的是登录方式，不是数据：该密钥名下的私有片段仍可被同一主人的
// 其他凭证读取。
func (c *Client) RevokeKey(ctx context.Context, id string) error {
	if err := validateKeyID(id); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodDelete, pathKeys+"/"+id, "", nil, nil)
	return err
}

// validateKeyID 挡住会改变路径结构的字符。
//
// 密钥 id 形如 ULID，正常不会含这些字符；但拼进 URL 之前挡住，
// 好过让签名与预期路径悄悄错位。
func validateKeyID(id string) error {
	if id == "" {
		return fmt.Errorf("hubclient: 密钥 id 不能为空")
	}
	if strings.ContainsAny(id, "/?#%") {
		return fmt.Errorf("hubclient: 密钥 id 含非法字符: %q", id)
	}
	return nil
}
