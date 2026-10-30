package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
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
// extension_url 非空时只走 extension，不做直连回退：与 RSS 抓取保持同一策略，
// 避免运维以为流量走 extension、实际却绕过它直连外网。
func newAvatarSource(cfg *config.Config) avatarSource {
	timeout := 10 * time.Second
	if cfg != nil && cfg.Gravatar.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Gravatar.TimeoutSeconds) * time.Second
	}

	if cfg != nil && cfg.Gravatar.ExtensionURL != "" {
		return &extensionAvatarSource{
			client: &http.Client{
				Timeout: timeout,
				// extension 地址由运维配置，跳转不应发生：跳转只会掩盖配置错误。
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			},
			endpoint: cfg.Gravatar.ExtensionURL,
			token:    cfg.Gravatar.ExtensionToken,
		}
	}

	var proxyNames []string
	if cfg != nil {
		proxyNames = cfg.Gravatar.ProxyNames
	}
	transport, err := config.TransportFor(proxyNames)
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
	}
}

// ============================================================
// 直连 Gravatar
// ============================================================

type directAvatarSource struct {
	client  *http.Client
	baseURL string
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
		return nil, false, fmt.Errorf("%w: %v", ErrAvatarUpstream, err)
	}
	defer resp.Body.Close()

	return readAvatarResponse(resp, "gravatar")
}

// ============================================================
// extension 图片代理
// ============================================================

// extensionAvatarSource 通过外部 extension 服务获取头像。
//
// 契约（见 docs/extension.md）：
//
//	GET {extension_url}/avatar/{hash}?s={size}&d={style}&r={rating}
//	Authorization: Bearer {token}
//
// extension 负责代 HomeHub 访问官方 Gravatar（网络条件更好或已缓存），
// 但仍要把上游的 Content-Type/ETag/Last-Modified 透传回来。
type extensionAvatarSource struct {
	client   *http.Client
	endpoint string
	token    string
}

func (s *extensionAvatarSource) Key() string {
	// endpoint 可能带凭据或内网地址，用短哈希避免写进缓存行与日志。
	sum := sha256.Sum256([]byte(s.endpoint))
	return "ext:" + hex.EncodeToString(sum[:6])
}

func (s *extensionAvatarSource) buildURL(req avatarRequest) string {
	q := url.Values{}
	q.Set("d", req.Style)
	q.Set("r", req.Rating)
	q.Set("s", strconv.Itoa(req.Size))
	return strings.TrimRight(s.endpoint, "/") + "/avatar/" + url.PathEscape(req.Hash) + "?" + q.Encode()
}

func (s *extensionAvatarSource) Fetch(ctx context.Context, req avatarRequest, known *avatarConditions) (*avatarPayload, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.buildURL(req), nil)
	if err != nil {
		return nil, false, fmt.Errorf("%w: 构造 extension 请求失败: %v", ErrAvatarUpstream, err)
	}
	httpReq.Header.Set("Accept", "image/png, image/jpeg, image/gif, image/webp")
	if s.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.token)
	}
	applyAvatarConditions(httpReq, known)

	resp, err := s.client.Do(httpReq)
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
