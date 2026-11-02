package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/hubclient"
	"github.com/ignvex/HomeHub/internal/logger"
)

// ============================================================
// 头像错误分类
// ============================================================

// 这些哨兵错误让 handler 能把「用户不存在」「上游故障」「内容不合法」
// 映射成不同状态码，而不是所有失败都返回 404。
var (
	// ErrGravatarDisabled 表示配置中关闭了 Gravatar 头像功能。
	ErrGravatarDisabled = errors.New("Gravatar 头像功能已禁用")
	// ErrAvatarUnavailable 表示用户存在但不具备生成头像的条件（未设置邮箱）。
	// 「用户不存在」复用 auth_service.go 的 ErrUserNotFound，避免两套同义哨兵。
	ErrAvatarUnavailable = errors.New("用户未设置邮箱")
	// ErrAvatarNotFound 表示上游明确返回 404（例如 default_style=404 且无自定义头像）。
	ErrAvatarNotFound = errors.New("上游没有该头像")
	// ErrAvatarUpstream 表示上游网络错误、超时或非 2xx 响应。
	ErrAvatarUpstream = errors.New("头像上游请求失败")
	// ErrAvatarInvalid 表示上游响应不是受支持的位图（可能是 HTML 错误页或 SVG）。
	ErrAvatarInvalid = errors.New("上游返回的头像格式不合法")
	// ErrAvatarEndpointHTML 表示 avatar endpoint 返回了一个 HTML 页面。
	//
	// 与 ErrAvatarInvalid 分开是因为原因不同：后者是「对方给了个坏图片」，
	// 前者几乎总是「extension_url 指向了一个没有 /avatar 端点的服务」。
	// 两者的修法完全不同，压成一句话会把人送去查图片格式。
	ErrAvatarEndpointHTML = errors.New("头像端点返回了 HTML 页面，检查 extension_url")
)

// ============================================================
// 上游契约
// ============================================================

const (
	// gravatarDefaultBaseURL 是 Gravatar 官方头像服务地址。
	gravatarDefaultBaseURL = "https://www.gravatar.com/avatar"
	// gravatarMaxRedirects 限制直连模式的重定向次数。
	gravatarMaxRedirects = 3
	// defaultGravatarMaxBytes 是配置缺失时的兜底大小上限。
	defaultGravatarMaxBytes = 2 << 20
)

// avatarRequest 描述一次头像下载的参数。
type avatarRequest struct {
	Hash   string
	Size   int
	Style  string
	Rating string
}

// avatarConditions 是条件请求的已知缓存元数据。
type avatarConditions struct {
	ETag         string
	LastModified string
}

// avatarPayload 是校验通过的上游响应。
type avatarPayload struct {
	Data         []byte
	ContentType  string
	ETag         string
	LastModified string
}

// avatarSource 是头像的唯一图片来源（官方 Gravatar 或 extension）。
//
// 实现必须自行完成响应校验：调用方不信任上游返回的任何元数据。
type avatarSource interface {
	// Key 标识来源身份与网络参数，用于构造缓存变体。
	// 配置变化（换 extension、换代理、换超时）时它必须随之变化。
	Key() string
	// Fetch 拉取头像。known 非空时携带条件头；
	// 返回 notModified=true 表示上游确认可复用本地缓存。
	Fetch(ctx context.Context, req avatarRequest, known *avatarConditions) (payload *avatarPayload, notModified bool, err error)
}

// newAvatarSource 按当前配置选择图片来源。
//
// 只有 extension.features.avatar 为真且连接可用时才走 extension，
// 否则直连 Gravatar（走全局代理）。不做自动回退：开关打开后若
// Extension 不可用，错误会明确报出来，而不是偷偷直连——那会让运维
// 以为流量走了 Extension。
func newAvatarSource(cfg *config.Config) avatarSource {
	timeout := 10 * time.Second
	if cfg != nil && cfg.Gravatar.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Gravatar.TimeoutSeconds) * time.Second
	}

	if cfg != nil && cfg.Extension.Features.Avatar {
		// 开关打开后不再有静默直连的退路。
		//
		// 之前构造失败会继续往下走直连：流量明明没走 Extension，界面却
		// 显示「已启用」，运维以为代理生效了。构造失败本身就是可诊断的
		// 状态，让它以错误的形式出现在调用处更有用。
		client, err := buildExtensionClient(cfg, timeout)
		if err != nil {
			return &brokenAvatarSource{key: "broken|" + cfg.Extension.BaseURL, cause: err}
		}
		return &extensionAvatarSource{
			client:   client,
			endpoint: cfg.Extension.BaseURL,
		}
	}

	transport, err := config.DefaultTransport()
	if err != nil {
		logger.Warn("[gravatar] 代理配置不可用，改为直连", "error", err)
		transport, _ = config.TransportFor(nil)
	}

	return &directAvatarSource{
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// Gravatar 会 301 到 secure.gravatar.com，需要跟随；
			// 但只允许 http(s)，并限制次数，避免异常跳转。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= gravatarMaxRedirects {
					return errors.New("头像请求重定向次数过多")
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return fmt.Errorf("拒绝跳转到非 http(s) 地址: %s", req.URL.Scheme)
				}
				return nil
			},
		},
		baseURL: gravatarDefaultBaseURL,
		// proxyConfigured 只用于「超时且未配代理」时的诊断提示。
		// 现在代理是全局的，判断依据随之改为全局默认链。
		proxyConfigured: len(config.ResolveProxy(defaultProxyNames())) > 0,
	}
}

// defaultProxyNames 返回全局默认代理链，供诊断文案判断「到底配没配代理」。
func defaultProxyNames() []string {
	if cfg := config.Get(); cfg != nil {
		return cfg.Proxy.Default
	}
	return nil
}

// brokenAvatarSource 表示「已声明交给 Extension 但连接不可用」。
//
// 它不做任何网络请求，只在 Fetch 时把配置错误原样抛出。存在的意义是
// 让「开关打开但配置有问题」变成显式失败，而不是悄悄退回直连。
type brokenAvatarSource struct {
	key   string
	cause error
}

func (s *brokenAvatarSource) Key() string { return s.key }

func (s *brokenAvatarSource) Fetch(context.Context, avatarRequest, *avatarConditions) (*avatarPayload, bool, error) {
	return nil, false, fmt.Errorf("%w: extension.features.avatar 已开启但连接不可用: %v", ErrAvatarUpstream, s.cause)
}

// ============================================================
// 直连 Gravatar
// ============================================================

type directAvatarSource struct {
	client  *http.Client
	baseURL string
	// proxyConfigured 记录本次直连是否配了代理。
	//
	// 只用于失败时的诊断提示：超时且无代理，几乎总是配置漏了，
	// 而不是 Gravatar 挂了。
	proxyConfigured bool
}

// isTimeoutError 判断错误是否为超时。
//
// 超时在 Go 里有三种包装形式：context.DeadlineExceeded、
// net.Error.Timeout()、以及 http.Client.Timeout 触发的 url.Error。
// 逐个判断，不依赖错误字符串匹配。
func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func (s *directAvatarSource) Key() string {
	return "direct:" + s.baseURL
}

func (s *directAvatarSource) buildURL(req avatarRequest) string {
	q := url.Values{}
	q.Set("d", req.Style)
	q.Set("r", req.Rating)
	q.Set("s", strconv.Itoa(req.Size))
	return strings.TrimRight(s.baseURL, "/") + "/" + url.PathEscape(req.Hash) + "?" + q.Encode()
}

func (s *directAvatarSource) Fetch(ctx context.Context, req avatarRequest, known *avatarConditions) (*avatarPayload, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.buildURL(req), nil)
	if err != nil {
		return nil, false, fmt.Errorf("%w: 构造请求失败: %v", ErrAvatarUpstream, err)
	}
	httpReq.Header.Set("Accept", "image/png, image/jpeg, image/gif, image/webp")
	applyAvatarConditions(httpReq, known)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		// 超时且没配代理时补一句指向配置的提示。
		//
		// 这个组合很常见：部署在需要代理才能出网的环境里，
		// gravatar.proxy_names 忘了填。日志里只有一句
		// "context deadline exceeded"，看不出是网络不通还是没配代理，
		// 排查往往会先去怀疑 Gravatar 挂了。
		if !s.proxyConfigured && isTimeoutError(err) {
			return nil, false, fmt.Errorf("%w: %v（gravatar.proxy_names 为空，当前为直连；若本机需经代理出网，请在配置的 proxy 池中定义代理并让 gravatar 引用它）", ErrAvatarUpstream, err)
		}
		return nil, false, fmt.Errorf("%w: %v", ErrAvatarUpstream, err)
	}
	defer resp.Body.Close()

	return readAvatarResponse(resp, "gravatar")
}

// ============================================================
// extension 图片代理
// ============================================================

// extensionAvatarSource 通过 HomeHub Extension 获取头像。
//
// 契约（见 docs/extension.md）：
//
//	GET {base}/avatar/{hash}?s={size}&d={style}&r={rating}
//	Ed25519 签名头（与 /hub/* 同一把密钥）
//
// extension 负责代 HomeHub 访问官方 Gravatar（网络条件更好或已缓存），
// 但仍要把上游的 Content-Type/ETag/Last-Modified 透传回来。
//
// 认证从 Bearer 换成签名是本次重构的一部分：同一个对端不该有两套凭据。
type extensionAvatarSource struct {
	client *hubclient.Client
	// endpoint 仅用于 Key()，参与配置指纹计算。
	endpoint string
}

func (s *extensionAvatarSource) Key() string {
	// endpoint 可能带凭据或内网地址，用短哈希避免写进缓存行与日志。
	sum := sha256.Sum256([]byte(s.endpoint))
	return "ext:" + hex.EncodeToString(sum[:6])
}

func (s *extensionAvatarSource) Fetch(ctx context.Context, req avatarRequest, known *avatarConditions) (*avatarPayload, bool, error) {
	q := url.Values{}
	q.Set("d", req.Style)
	q.Set("r", req.Rating)
	q.Set("s", strconv.Itoa(req.Size))

	extra := http.Header{}
	if known != nil {
		if known.ETag != "" {
			extra.Set("If-None-Match", known.ETag)
		}
		if known.LastModified != "" {
			extra.Set("If-Modified-Since", known.LastModified)
		}
	}

	resp, err := s.client.DoRaw(ctx, http.MethodGet,
		"/avatar/"+url.PathEscape(req.Hash), q.Encode(),
		"image/png, image/jpeg, image/gif, image/webp", extra)
	if err != nil {
		return nil, false, fmt.Errorf("%w: extension 请求失败: %v", ErrAvatarUpstream, err)
	}
	defer resp.Body.Close()

	return readAvatarResponse(resp, "extension")
}

// ============================================================
// 响应处理与校验
// ============================================================

func applyAvatarConditions(req *http.Request, known *avatarConditions) {
	if known == nil {
		return
	}
	if known.ETag != "" {
		req.Header.Set("If-None-Match", known.ETag)
	}
	if known.LastModified != "" {
		req.Header.Set("If-Modified-Since", known.LastModified)
	}
}

// readAvatarResponse 把上游响应转换成经过校验的 payload。
//
// 所有来源共用这一段：状态码分类、大小上限、图片魔数校验都在这里，
// 避免 extension 成为绕过校验的新入口。
func readAvatarResponse(resp *http.Response, origin string) (*avatarPayload, bool, error) {
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil, true, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, false, ErrAvatarNotFound
	case resp.StatusCode != http.StatusOK:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, false, fmt.Errorf("%w: %s 返回 HTTP %d: %s",
			ErrAvatarUpstream, origin, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	data, err := readAvatarBody(resp.Body, avatarMaxBytes())
	if err != nil {
		return nil, false, err
	}

	contentType, err := validateAvatarImage(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w（%s 声明为 %q）", err, origin, resp.Header.Get("Content-Type"))
	}

	return &avatarPayload{
		Data:         data,
		ContentType:  contentType,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, false, nil
}

// readAvatarBody 读取响应体，超过上限时返回错误而不是截断。
//
// 截断会把半张图片写进缓存并长期返回损坏内容，因此必须显式失败。
func readAvatarBody(body io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultGravatarMaxBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: 读取响应失败: %v", ErrAvatarUpstream, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: 响应超过 %d 字节上限", ErrAvatarInvalid, maxBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: 响应为空", ErrAvatarInvalid)
	}
	return data, nil
}

// allowedAvatarContentTypes 是允许存入缓存的位图类型。
//
// 明确排除 image/svg+xml：SVG 是 XML，可内嵌 <script>，而头像接口会被
// 浏览器直接打开，等于把上游内容变成同源可执行脚本。
var allowedAvatarContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// validateAvatarImage 以字节嗅探判断真实类型，返回规范化 MIME。
//
// 不采用上游声明的 Content-Type 作为依据：源站或 extension 可能把 HTML
// 错误页标成 image/jpeg。嗅探无法识别时一律拒绝，宁可返回占位头像。
func validateAvatarImage(data []byte) (string, error) {
	detected := detectContentType(data)
	// http.DetectContentType 不识别 WebP，会返回 application/octet-stream。
	if detected == "application/octet-stream" && isWebP(data) {
		detected = "image/webp"
	}
	if allowedAvatarContentTypes[detected] {
		return detected, nil
	}
	// HTML 单独归类：这几乎总是「配错了 extension_url」而不是「图片坏了」。
	if strings.HasPrefix(detected, "text/html") {
		return "", fmt.Errorf("%w（嗅探到 %s）", ErrAvatarEndpointHTML, detected)
	}
	return "", fmt.Errorf("%w: 嗅探到 %s", ErrAvatarInvalid, detected)
}

// isWebP 按 RIFF 容器格式判断 WebP。
func isWebP(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

// avatarMaxBytes 读取当前配置的头像大小上限（配置热更新即时生效）。
func avatarMaxBytes() int64 {
	if cfg := config.Get(); cfg != nil && cfg.Gravatar.MaxBytes > 0 {
		return cfg.Gravatar.MaxBytes
	}
	return defaultGravatarMaxBytes
}
