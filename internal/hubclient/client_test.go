package hubclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 本文件从服务端视角验签：测试服务器不读客户端的实现，而是拿请求本身
// 重算一遍待签消息。客户端若签了 A 却发出 B，这里就会失败 —— 这是
// 唯一能证明"签名覆盖了实际发出的字节"的测法。

// testSigner 生成一对固定的测试密钥。
func testSigner(t *testing.T) (*Signer, ed25519.PublicKey) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, ed25519.SeedSize))
	signer, err := NewSigner(key)
	if err != nil {
		t.Fatalf("构造签名器失败: %v", err)
	}
	return signer, key.Public().(ed25519.PublicKey)
}

// signedServer 启动一个强制验签的测试服务器。
//
// 验签失败直接 401 并记录原因，handler 只在签名通过后执行。
func signedServer(t *testing.T, pub ed25519.PublicKey, handler http.HandlerFunc) (*httptest.Server, *signatureRecorder) {
	t.Helper()

	rec := &signatureRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体失败: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		tsHeader := r.Header.Get(headerTimestamp)
		nonce := r.Header.Get(headerNonce)
		sigHeader := r.Header.Get(headerSignature)

		rec.record(r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body, tsHeader, nonce, r.Header.Get("Content-Type"))

		if tsHeader == "" || nonce == "" || sigHeader == "" {
			t.Errorf("缺少签名头: ts=%q nonce=%q sig=%q", tsHeader, nonce, sigHeader)
			writeProtocolError(w, http.StatusUnauthorized, CodeUnauthenticated, "签名校验失败")
			return
		}

		ts, err := strconv.ParseInt(tsHeader, 10, 64)
		if err != nil {
			t.Errorf("时间戳不是整数: %q", tsHeader)
			writeProtocolError(w, http.StatusUnauthorized, CodeUnauthenticated, "时间戳格式错误")
			return
		}
		if skew := time.Since(time.Unix(ts, 0)); skew > time.Minute || skew < -time.Minute {
			t.Errorf("时间戳偏差过大: %v", skew)
		}

		sig, err := base64.StdEncoding.DecodeString(sigHeader)
		if err != nil {
			t.Errorf("签名不是合法 base64: %v", err)
			writeProtocolError(w, http.StatusUnauthorized, CodeUnauthenticated, "签名编码错误")
			return
		}

		msg := Message(r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body, ts, nonce)
		if !ed25519.Verify(pub, []byte(msg), sig) {
			t.Errorf("验签失败，服务端重算的消息为:\n%s", msg)
			writeProtocolError(w, http.StatusUnauthorized, CodeUnauthenticated, "签名校验失败")
			return
		}

		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// signatureRecorder 记录每次请求中与签名相关的原始素材。
type signatureRecorder struct {
	mu      sync.Mutex
	nonces  []string
	methods []string
	paths   []string
	queries []string
	bodies  []string
	types   []string
}

func (s *signatureRecorder) record(method, path, query string, body []byte, _, nonce, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonces = append(s.nonces, nonce)
	s.methods = append(s.methods, method)
	s.paths = append(s.paths, path)
	s.queries = append(s.queries, query)
	s.bodies = append(s.bodies, string(body))
	s.types = append(s.types, contentType)
}

func writeProtocolError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `","request_id":"test-rid"}}`))
}

// clientFor 用测试服务器地址构造客户端，base 带上 /api/v1 前缀。
func clientFor(t *testing.T, srvURL string, signer *Signer) *Client {
	t.Helper()
	client, err := New(srvURL+"/api/v1", signer)
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	return client
}

// ---------- 签名覆盖 ----------

// TestEveryEndpointIsSignedAndAccepted 让七个端点各跑一遍，全部经过服务端验签。
func TestEveryEndpointIsSignedAndAccepted(t *testing.T) {
	signer, pub := testSigner(t)

	srv, rec := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/hub/subscriptions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"url":"https://example.com/feed.xml","fetch_interval":"30m","enabled":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/hub/subscriptions":
			_, _ = w.Write([]byte(`{"items":[{"id":7,"url":"https://example.com/feed.xml"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/hub/subscriptions/7":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/hub/subscriptions/7/refresh":
			_, _ = w.Write([]byte(`{"feed_id":7,"new_entries":3,"status":200}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/hub/keys":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"01JC8Z","name":"alice","key":"HHE-XXXX","live":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/hub/keys":
			_, _ = w.Write([]byte(`{"items":[{"id":"01JC8Z","name":"alice","live":true}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/hub/keys/01JC8Z":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := clientFor(t, srv.URL, signer)
	ctx := t.Context()

	if _, _, err := client.UpsertSubscription(ctx, UpsertSubscriptionRequest{URL: "https://example.com/feed.xml"}); err != nil {
		t.Fatalf("推送订阅失败: %v", err)
	}
	if _, err := client.ListSubscriptions(ctx); err != nil {
		t.Fatalf("列出订阅失败: %v", err)
	}
	if err := client.DeleteSubscription(ctx, 7); err != nil {
		t.Fatalf("删除订阅失败: %v", err)
	}
	if _, err := client.RefreshSubscription(ctx, 7); err != nil {
		t.Fatalf("刷新订阅失败: %v", err)
	}
	if _, err := client.CreateKey(ctx, CreateKeyRequest{Name: "alice"}); err != nil {
		t.Fatalf("签发密钥失败: %v", err)
	}
	if _, err := client.ListKeys(ctx); err != nil {
		t.Fatalf("列出密钥失败: %v", err)
	}
	if err := client.RevokeKey(ctx, "01JC8Z"); err != nil {
		t.Fatalf("撤销密钥失败: %v", err)
	}

	if len(rec.paths) != 7 {
		t.Fatalf("服务端收到 %d 个请求，期望 7", len(rec.paths))
	}

	// 时间戳与 nonce 每次都必须是新的：重放会让 Extension 判为重复。
	seen := map[string]struct{}{}
	for _, nonce := range rec.nonces {
		if nonce == "" {
			t.Error("出现空 nonce")
		}
		if _, dup := seen[nonce]; dup {
			t.Errorf("nonce 重复: %s", nonce)
		}
		seen[nonce] = struct{}{}
	}
}

// TestNoBodyEndpointsSendNoBody 确认 GET / DELETE 不带请求体与 Content-Type。
//
// 带一个空的 JSON 体不算错，但会和"摘要按空串算"的约定混在一起，
// 排查时容易误判。协议要求空 body 就是不发送。
func TestNoBodyEndpointsSendNoBody(t *testing.T) {
	signer, pub := testSigner(t)
	srv, rec := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	client := clientFor(t, srv.URL, signer)
	ctx := t.Context()

	if _, err := client.ListSubscriptions(ctx); err != nil {
		t.Fatalf("列出订阅失败: %v", err)
	}
	if err := client.DeleteSubscription(ctx, 7); err != nil {
		t.Fatalf("删除订阅失败: %v", err)
	}

	for i, body := range rec.bodies {
		if body != "" {
			t.Errorf("第 %d 个请求带了 body: %q", i+1, body)
		}
	}
	for i, ct := range rec.types {
		if ct != "" {
			t.Errorf("第 %d 个请求带了 Content-Type: %q", i+1, ct)
		}
	}
}

// TestBodySentMatchesSignedBytes 确认发出去的字节就是签名的字节。
//
// 服务端验签已经覆盖了这一点；这里额外断言 JSON 里省略字段的语义，
// 因为"省略 = 保留原值"是协议的行为约定。
func TestBodySentMatchesSignedBytes(t *testing.T) {
	signer, pub := testSigner(t)
	srv, rec := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"url":"https://example.com/f.xml"}`))
	})
	client := clientFor(t, srv.URL, signer)

	interval := "30m"
	if _, _, err := client.UpsertSubscription(t.Context(), UpsertSubscriptionRequest{
		URL:      "https://example.com/f.xml",
		Interval: &interval,
	}); err != nil {
		t.Fatalf("推送订阅失败: %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.bodies[0]), &sent); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if sent["url"] != "https://example.com/f.xml" || sent["interval"] != "30m" {
		t.Errorf("请求体字段不符: %v", sent)
	}
	for _, omitted := range []string{"title", "enabled", "fetch_now"} {
		if _, ok := sent[omitted]; ok {
			t.Errorf("未设置的字段 %q 不应出现在请求体里（省略表示保留原值）", omitted)
		}
	}
}

// TestExplicitFalseIsSent 确认显式 false 不会被 omitempty 吞掉。
//
// enabled=false 是"停用这个订阅"的有效意图，与"没提这个字段"完全不同。
func TestExplicitFalseIsSent(t *testing.T) {
	signer, pub := testSigner(t)
	srv, rec := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"enabled":false}`))
	})
	client := clientFor(t, srv.URL, signer)

	disabled := false
	if _, _, err := client.UpsertSubscription(t.Context(), UpsertSubscriptionRequest{
		URL:     "https://example.com/f.xml",
		Enabled: &disabled,
	}); err != nil {
		t.Fatalf("推送订阅失败: %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.bodies[0]), &sent); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	value, ok := sent["enabled"]
	if !ok {
		t.Fatal("显式的 enabled=false 被省略了")
	}
	if value != false {
		t.Errorf("enabled = %v，期望 false", value)
	}
}

// ---------- 响应语义 ----------

func TestUpsertDistinguishesCreatedFromUpdated(t *testing.T) {
	signer, pub := testSigner(t)

	cases := []struct {
		status  int
		created bool
	}{
		{http.StatusCreated, true},
		{http.StatusOK, false},
	}
	for _, tc := range cases {
		status := tc.status
		srv, _ := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"id":7,"url":"https://example.com/f.xml","fetch_interval":"30m"}`))
		})
		sub, created, err := clientFor(t, srv.URL, signer).
			UpsertSubscription(t.Context(), UpsertSubscriptionRequest{URL: "https://example.com/f.xml"})
		if err != nil {
			t.Fatalf("status=%d: 推送失败: %v", status, err)
		}
		if created != tc.created {
			t.Errorf("status=%d: created=%v，期望 %v", status, created, tc.created)
		}
		if sub.ID != 7 || sub.FetchInterval != "30m" {
			t.Errorf("status=%d: 响应解析错误: %+v", status, sub)
		}
		srv.Close()
	}
}

// TestListNormalizesNullItems 确认 items 为 null 时返回空切片。
//
// 协议说 items 始终是数组，但客户端不该因为对端的一次偏差就 panic
// 或让调用方对 nil 做额外判空。
func TestListNormalizesNullItems(t *testing.T) {
	signer, pub := testSigner(t)
	srv, _ := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":null}`))
	})
	items, err := clientFor(t, srv.URL, signer).ListSubscriptions(t.Context())
	if err != nil {
		t.Fatalf("列出订阅失败: %v", err)
	}
	if items == nil {
		t.Fatal("返回了 nil 切片，期望空切片")
	}
	if len(items) != 0 {
		t.Errorf("条数 = %d，期望 0", len(items))
	}
}

// ---------- 错误映射 ----------

func TestErrorMapping(t *testing.T) {
	signer, pub := testSigner(t)

	cases := []struct {
		name      string
		status    int
		code      string
		retryable bool
		notFound  bool
		unauth    bool
	}{
		{"400 请求格式错误", http.StatusBadRequest, CodeInvalidRequest, false, false, false},
		{"401 验签失败", http.StatusUnauthorized, CodeUnauthenticated, false, false, true},
		{"404 不存在", http.StatusNotFound, CodeNotFound, false, true, false},
		{"413 体积超限", http.StatusRequestEntityTooLarge, CodePayloadTooLarge, false, false, false},
		{"429 限流", http.StatusTooManyRequests, CodeRateLimited, true, false, false},
		{"500 内部错误", http.StatusInternalServerError, CodeInternal, true, false, false},
		{"502 上游失败", http.StatusBadGateway, CodeUpstreamError, true, false, false},
		{"503 通道未就绪", http.StatusServiceUnavailable, CodeUnavailable, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code := tc.status, tc.code
			srv, _ := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
				writeProtocolError(w, status, code, "detail")
			})

			_, err := clientFor(t, srv.URL, signer).ListSubscriptions(t.Context())
			srv.Close()

			if err == nil {
				t.Fatal("期望返回错误")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("错误类型 = %T，期望 *APIError: %v", err, err)
			}
			if apiErr.StatusCode != tc.status || apiErr.Code != tc.code {
				t.Errorf("StatusCode/Code = %d/%s，期望 %d/%s", apiErr.StatusCode, apiErr.Code, tc.status, tc.code)
			}
			if apiErr.RequestID != "test-rid" {
				t.Errorf("RequestID = %q，期望 test-rid", apiErr.RequestID)
			}
			if apiErr.Message != "detail" {
				t.Errorf("Message = %q", apiErr.Message)
			}
			if got := IsRetryable(err); got != tc.retryable {
				t.Errorf("IsRetryable = %v，期望 %v", got, tc.retryable)
			}
			if got := IsNotFound(err); got != tc.notFound {
				t.Errorf("IsNotFound = %v，期望 %v", got, tc.notFound)
			}
			if got := IsUnauthenticated(err); got != tc.unauth {
				t.Errorf("IsUnauthenticated = %v，期望 %v", got, tc.unauth)
			}
		})
	}
}

// TestRateLimitCarriesRetryAfter 确认 429 的 Retry-After 被解析出来。
func TestRateLimitCarriesRetryAfter(t *testing.T) {
	signer, pub := testSigner(t)
	srv, _ := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		writeProtocolError(w, http.StatusTooManyRequests, CodeRateLimited, "慢一点")
	})

	_, err := clientFor(t, srv.URL, signer).ListSubscriptions(t.Context())
	if err == nil {
		t.Fatal("期望返回错误")
	}
	delay, ok := RetryAfterOf(err)
	if !ok {
		t.Fatal("未解析出 Retry-After")
	}
	if delay != 120*time.Second {
		t.Errorf("Retry-After = %v，期望 2m", delay)
	}
}

// TestNonProtocolErrorKeepsSnippet 确认网关返回的 HTML/纯文本不会丢成空消息。
func TestNonProtocolErrorKeepsSnippet(t *testing.T) {
	signer, pub := testSigner(t)
	srv, _ := signedServer(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream connect error"))
	})

	_, err := clientFor(t, srv.URL, signer).ListSubscriptions(t.Context())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型 = %T", err)
	}
	if apiErr.Code != CodeUpstreamError {
		t.Errorf("Code = %q，期望兜底的 %q", apiErr.Code, CodeUpstreamError)
	}
	if !strings.Contains(apiErr.Message, "upstream connect error") {
		t.Errorf("Message 未保留响应片段: %q", apiErr.Message)
	}
}

// TestNetworkErrorIsRetryable 确认传输层失败被判定为可重试。
func TestNetworkErrorIsRetryable(t *testing.T) {
	signer, _ := testSigner(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // 立刻关掉，制造连接失败

	client, err := New(base+"/api/v1", signer)
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	client.Timeout = 2 * time.Second

	_, listErr := client.ListSubscriptions(t.Context())
	if listErr == nil {
		t.Fatal("期望连接失败")
	}
	if !IsRetryable(listErr) {
		t.Errorf("连接失败应可重试: %v", listErr)
	}
}

// TestCanceledContextIsNotRetryable 确认上下文取消不被当作可重试。
func TestCanceledContextIsNotRetryable(t *testing.T) {
	signer, _ := testSigner(t)
	client, err := New("http://127.0.0.1:1/api/v1", signer)
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ListSubscriptions(ctx); err == nil {
		t.Fatal("期望请求失败")
	} else if IsRetryable(err) {
		t.Errorf("上下文取消不应可重试: %v", err)
	}
}

// ---------- 构造参数校验 ----------

func TestNewValidatesBaseURL(t *testing.T) {
	signer, _ := testSigner(t)

	cases := []string{"", "ftp://example.com", "example.com", "http://", "://bad"}
	for _, base := range cases {
		if _, err := New(base, signer); err == nil {
			t.Errorf("base=%q 应被拒绝", base)
		}
	}

	if _, err := New("https://example.com/api/v1", nil); err == nil {
		t.Error("nil 签名器应被拒绝")
	}
}

func TestValidateKeyIDRejectsPathInjection(t *testing.T) {
	for _, id := range []string{"", "a/b", "a?b", "a#b", "a%b"} {
		if err := validateKeyID(id); err == nil {
			t.Errorf("id=%q 应被拒绝", id)
		}
	}
	if err := validateKeyID("01JC8ZABC"); err != nil {
		t.Errorf("合法 id 被拒绝: %v", err)
	}
}

func TestCreateKeyRequiresName(t *testing.T) {
	signer, _ := testSigner(t)
	client, err := New("https://example.com/api/v1", signer)
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	if _, err := client.CreateKey(t.Context(), CreateKeyRequest{}); err == nil {
		t.Error("空 name 应被拒绝")
	}
}

func TestUpsertRequiresURL(t *testing.T) {
	signer, _ := testSigner(t)
	client, err := New("https://example.com/api/v1", signer)
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	if _, _, err := client.UpsertSubscription(t.Context(), UpsertSubscriptionRequest{}); err == nil {
		t.Error("空 url 应被拒绝")
	}
}
