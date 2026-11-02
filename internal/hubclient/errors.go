package hubclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// 协议文档 §5 定义的错误码。Extension 只保证这些值，宿主按它们决策，
// 不要靠 message 文本判断。
const (
	CodeInvalidRequest  = "invalid_request"
	CodeUnauthenticated = "unauthenticated"
	CodeNotFound        = "not_found"
	CodePayloadTooLarge = "payload_too_large"
	CodeRateLimited     = "rate_limited"
	CodeInternal        = "internal"
	CodeUpstreamError   = "upstream_error"
	CodeUnavailable     = "unavailable"
)

// APIError 是 Extension 返回的结构化错误。
type APIError struct {
	StatusCode int           // HTTP 状态码
	Code       string        // 协议错误码，见上方常量
	Message    string        // 服务端给的说明，仅供人读
	RequestID  string        // 与服务端 JSON 日志中的 request_id 一致
	RetryAfter time.Duration // 仅 429 可能非零
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("hubclient: HTTP %d %s: %s", e.StatusCode, e.Code, e.Message)
	if e.RetryAfter > 0 {
		// 限流与临时不可用会给出建议等待时间。不写出来的话，读日志的人
		// 只看到「429」和一句服务端说明，不知道该等多久、是源的问题
		// 还是自己打得太快。
		msg += fmt.Sprintf("（建议 %s 后重试）", e.RetryAfter)
	}
	if e.RequestID != "" {
		msg += "（request_id=" + e.RequestID + "）"
	}
	return msg
}

// ErrorFromResponse 把一条非 2xx 响应转成 *APIError。
//
// 供 DoRaw 的调用方使用：DoRaw 不自动判定成功与否（头像要把 304、404
// 分开对待），但那些确实算失败的响应仍需解析成统一结构，才能拿到
// 协议错误码、request_id 与 Retry-After。只读前 512 字节，避免异常
// 响应把内存吃满。
func ErrorFromResponse(resp *http.Response) *APIError {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return newAPIError(resp, snippet)
}

// IsNotFound 判断目标订阅或密钥不存在，也覆盖"RSS 模块未启用"。
//
// 协议特意让这两种情况共用 404：宿主据此从本地状态里移除条目，
// 或在模块关闭时收起 RSS 界面。
func IsNotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsUnauthenticated 判断签名/时钟/nonce 层面的失败。
//
// 这类错误重试没有意义：同样的实现再签一次仍然是 401。应当检查
// 时钟偏差、nonce 唯一性，以及 PATH/QUERY 的编码是否与发出的请求一致。
func IsUnauthenticated(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized
	}
	return false
}

// IsRetryable 给出协议推荐的重试判断。
//
// 只有 429、500、502 以及网络层错误值得重试：
//   - 400 / 401 / 404 / 413 是请求或状态本身的问题，原样重发只会得到同样结果
//   - 503 是公钥未配置或进程启动中，属于部署问题，重试无用
//   - 上下文取消与超时是调用方的决定，不该由这里再试
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway:
			return true
		default:
			return false
		}
	}
	// 非 APIError 视为传输层失败（连接被拒、DNS、读写中断等）。
	// 上下文错误单独排除。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// RetryAfterOf 取出 429 响应的 Retry-After。
func RetryAfterOf(err error) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return apiErr.RetryAfter, true
	}
	return 0, false
}

// parseRetryAfter 解析 Retry-After 头，支持秒数与 HTTP 日期两种形式。
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
