package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/hubclient"
	"github.com/ignvex/HomeHub/internal/model"
)

// newTestSignerFile 生成一把临时 Ed25519 私钥并以 PKCS#8 PEM 落盘。
//
// 返回公钥是为了让测试能真的验签，而不是只看请求头非空。
func newTestSignerFile(t *testing.T) (path string, pub ed25519.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}

	path = filepath.Join(t.TempDir(), "hub.key")
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("写入私钥失败: %v", err)
	}
	return path, pub
}

// newTestHubService 构造一个指向 srv 的服务实例。
//
// db 留空：这里只测不碰数据库的路径（状态探测、远端删除）。
func newTestHubService(t *testing.T, srvURL, keyPath string) *ExtensionHubService {
	t.Helper()
	return &ExtensionHubService{
		cfg: func() config.ExtensionHubConfig {
			return config.ExtensionHubConfig{
				Enabled:        true,
				BaseURL:        srvURL,
				PrivateKeyPath: keyPath,
				TimeoutSeconds: 5,
			}
		},
	}
}

// writeAPIError 按协议的统一错误结构写响应。
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// ============================================================
// 间隔格式化
// ============================================================

func TestFormatHubIntervalProducesCompactDurations(t *testing.T) {
	// 输出必须和协议示例同形（"30m" / "1h30m" / "6h"）。
	// 用 time.Duration.String() 会得到 "1h0m0s"，虽然对方能解析，
	// 但日志与对账时很难和配置值对上。
	cases := []struct {
		name    string
		seconds int
		want    string
	}{
		{"整小时不写零分钟", 3600, "1h"},
		{"小时加分钟", 5400, "1h30m"},
		{"整分钟", 1800, "30m"},
		{"六小时", 21600, "6h"},
		{"不足一分钟原样用秒", 90, "90s"},
		{"非整分钟不凑整", 3661, "3661s"},
		{"零值交给调用方省略", 0, ""},
		{"负数同样省略", -5, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatHubInterval(tc.seconds); got != tc.want {
				t.Fatalf("formatHubInterval(%d) = %q，期望 %q", tc.seconds, got, tc.want)
			}
		})
	}
}

// ============================================================
// 字段映射的三态语义
// ============================================================

func TestFeedToHubUpsertOmitsEmptyTitle(t *testing.T) {
	// 本地还没抓到标题时不能推空串：Extension 用「省略」表达
	// 「保留已学到的标题」，推空串会把对方抓到的标题清掉。
	feed := &model.RSSFeed{URL: "https://example.com/feed.xml", Title: "   "}
	req := feedToHubUpsert(feed, nil, false)

	if req.Title != nil {
		t.Fatalf("标题为空时不应发送，实际 %q", *req.Title)
	}
	if req.URL != feed.URL {
		t.Fatalf("URL 必须原样传递，实际 %q", req.URL)
	}
}

func TestFeedToHubUpsertKeepsEnabledUnsetForRoutineSync(t *testing.T) {
	// enabled 省略 = 不碰对方状态。若这里擅自传 true，一次例行对账
	// 就会把用户在 Extension 侧手动暂停的源重新打开，并清零失败计数。
	feed := &model.RSSFeed{URL: "https://example.com/feed.xml", FetchInterval: 1800}
	req := feedToHubUpsert(feed, nil, false)

	if req.Enabled != nil {
		t.Fatalf("enabled 应为 nil（不覆盖），实际 %v", *req.Enabled)
	}
	if req.Interval == nil || *req.Interval != "30m" {
		t.Fatalf("interval 应为 30m，实际 %v", req.Interval)
	}
}

func TestFeedToHubUpsertSendsExplicitFetchNow(t *testing.T) {
	// fetch_now 省略等于 true，所以「不立即抓取」只能靠显式 false 表达。
	// 对账时必须为 false，否则一次同步会对 N 个源同时发起抓取。
	feed := &model.RSSFeed{URL: "https://example.com/feed.xml", FetchInterval: 3600}

	syncReq := feedToHubUpsert(feed, nil, false)
	if syncReq.FetchNow == nil || *syncReq.FetchNow {
		t.Fatal("对账推送必须显式 fetch_now=false")
	}

	pushReq := feedToHubUpsert(feed, nil, true)
	if pushReq.FetchNow == nil || !*pushReq.FetchNow {
		t.Fatal("手动推送必须显式 fetch_now=true")
	}
}

func TestFeedToHubUpsertOmitsZeroInterval(t *testing.T) {
	// 本地间隔为 0（未设置）时省略，让对方用自己的默认值。
	// 推 0 会被夹到下限，值合法但语义错——等于替用户做了选择。
	feed := &model.RSSFeed{URL: "https://example.com/feed.xml"}
	req := feedToHubUpsert(feed, nil, false)

	if req.Interval != nil {
		t.Fatalf("间隔为 0 时不应发送，实际 %q", *req.Interval)
	}
}

func TestFeedToHubUpsertPassesExplicitEnabled(t *testing.T) {
	// 显式 true 是唯一能恢复被自动停用订阅的表达：Extension 会一并
	// 清零 error_count，否则它下一次失败就立刻再次越线。
	feed := &model.RSSFeed{URL: "https://example.com/feed.xml", FetchInterval: 3600}
	enabled := true
	req := feedToHubUpsert(feed, &enabled, true)

	if req.Enabled == nil || !*req.Enabled {
		t.Fatal("显式 true 必须原样透传")
	}
}

// ============================================================
// 状态探测
// ============================================================

func TestStatusReportsRSSModuleDisabled(t *testing.T) {
	// 404 在协议里同时表示「订阅不存在」和「RSS 模块未启用」。
	// 因为 /hub/keys 不受 rss.enabled 影响，先拿它验签：keys 200 +
	// subscriptions 404 才能断言「签名通过但模块关着」。
	//
	// 若不先验 keys，一个写错的 base_url（/hub/* 根本不存在）会被
	// 误报成「对方关了 RSS」，用户会去改一个没问题的开关。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/hub/keys") {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			return
		}
		writeAPIError(w, http.StatusNotFound, hubclient.CodeNotFound, "RSS 模块未启用")
	}))
	defer srv.Close()

	keyPath, _ := newTestSignerFile(t)
	st := newTestHubService(t, srv.URL, keyPath).Status(context.Background())

	if !st.Enabled || !st.Configured {
		t.Fatalf("启用且配置齐全时应为 true，实际 enabled=%v configured=%v", st.Enabled, st.Configured)
	}
	if !st.Reachable {
		t.Fatal("收到 200/404 说明 HTTP 层是通的")
	}
	if !st.Authenticated {
		t.Fatal("keys 端点返回 200，应判定签名已通过")
	}
	if st.RSSAvailable {
		t.Fatal("模块关闭时 rss_available 必须为 false")
	}
	if st.ErrorCode != hubclient.CodeNotFound {
		t.Fatalf("error_code 应为 %q，实际 %q", hubclient.CodeNotFound, st.ErrorCode)
	}
}

// TestStatusTreatsMissingKeysEndpointAsWrongBaseURL 锁住「路径写错」与
// 「模块关闭」的区分。
//
// 两者都会返回 404，但修法完全不同：前者改 base_url，后者改对端配置。
// 把前者报成后者，用户会在错误的机器上改一个没问题的开关。
func TestStatusTreatsMissingKeysEndpointAsWrongBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟反向代理截掉了 /api/v1：/hub/* 全部不存在。
		writeAPIError(w, http.StatusNotFound, hubclient.CodeNotFound, "404 page not found")
	}))
	defer srv.Close()

	keyPath, _ := newTestSignerFile(t)
	st := newTestHubService(t, srv.URL, keyPath).Status(context.Background())

	if st.Authenticated {
		t.Fatal("keys 端点 404 不能判成签名通过")
	}
	if st.RSSAvailable {
		t.Fatal("更不能判成 RSS 可用")
	}
	if st.ErrorCode != "config" {
		t.Fatalf("error_code 应为 config（地址写错），实际 %q", st.ErrorCode)
	}
	if !strings.Contains(st.ErrorMessage, "/api/v1") {
		t.Fatalf("提示应指向 base_url 前缀，实际 %q", st.ErrorMessage)
	}
}

func TestStatusReportsUnauthenticatedOn401(t *testing.T) {
	// 401 不能推到 Authenticated：那会掩盖时钟偏差与密钥不匹配。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusUnauthorized, hubclient.CodeUnauthenticated, "签名校验失败")
	}))
	defer srv.Close()

	keyPath, _ := newTestSignerFile(t)
	st := newTestHubService(t, srv.URL, keyPath).Status(context.Background())

	if !st.Reachable {
		t.Fatal("401 说明 HTTP 通了，只是签名没过")
	}
	if st.Authenticated {
		t.Fatal("401 时不能判定签名通过")
	}
	if st.RSSAvailable {
		t.Fatal("签名没过时不该声称 RSS 可用")
	}
	if st.ErrorCode != hubclient.CodeUnauthenticated {
		t.Fatalf("error_code 应为 %q，实际 %q", hubclient.CodeUnauthenticated, st.ErrorCode)
	}
}

func TestStatusReportsNetworkFailure(t *testing.T) {
	// 连不上时 reachable 必须为 false：前端据此提示「地址写错了」，
	// 而不是把它当成签名问题。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 立刻关闭，制造连接拒绝

	keyPath, _ := newTestSignerFile(t)
	st := newTestHubService(t, url, keyPath).Status(context.Background())

	if !st.Enabled || !st.Configured {
		t.Fatal("配置层面仍然齐全")
	}
	if st.Reachable {
		t.Fatal("连接被拒时 reachable 必须为 false")
	}
	if st.ErrorCode != "network" {
		t.Fatalf("error_code 应为 network，实际 %q", st.ErrorCode)
	}
}

func TestStatusReportsDisabledWithoutTouchingNetwork(t *testing.T) {
	// 未启用时不该发起任何请求。用一个必然失败的地址证明这一点：
	// 若真的发了，error_code 会是 network 而不是 disabled。
	svc := &ExtensionHubService{
		cfg: func() config.ExtensionHubConfig {
			return config.ExtensionHubConfig{Enabled: false, BaseURL: "http://127.0.0.1:1/api/v1"}
		},
	}

	st := svc.Status(context.Background())
	if st.Enabled {
		t.Fatal("未启用时 enabled 必须为 false")
	}
	if st.ErrorCode != "disabled" {
		t.Fatalf("error_code 应为 disabled，实际 %q", st.ErrorCode)
	}
}

func TestStatusVerifiesSignatureAgainstConfiguredKey(t *testing.T) {
	// 这是整条链路的真正证明：不仅要求三个签名头存在，还要用配置的
	// 公钥验得过。只检查“头非空”会放过签名算法或待签消息写错的情况。
	keyPath, pub := newTestSignerFile(t)

	// 服务端用配置的同一把公钥验签：验不过就 401，Status 会报
	// unauthenticated，测试随之失败。这比“检查头非空”强得多——
	// 后者会放过签名算法或待签消息写错的情况。
	verify := func(r *http.Request) bool {
		body, _ := io.ReadAll(r.Body)
		ts, err := strconv.ParseInt(r.Header.Get("X-HH-Timestamp"), 10, 64)
		if err != nil {
			return false
		}
		msg := hubclient.Message(r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body, ts, r.Header.Get("X-HH-Nonce"))
		sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-HH-Signature"))
		if err != nil {
			return false
		}
		return ed25519.Verify(pub, []byte(msg), sig)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verify(r) {
			writeAPIError(w, http.StatusUnauthorized, hubclient.CodeUnauthenticated, "签名校验失败")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer srv.Close()

	st := newTestHubService(t, srv.URL, keyPath).Status(context.Background())
	if !st.Authenticated {
		t.Fatalf("签名头应当通过校验，实际 error_code=%q message=%q", st.ErrorCode, st.ErrorMessage)
	}
	if st.SubscriptionCount != 0 {
		t.Fatalf("空列表应计 0 条，实际 %d", st.SubscriptionCount)
	}
}

func TestStatusSignatureIsRejectedWhenKeyMismatches(t *testing.T) {
	// 反证：换一把公钥验签必须失败，否则上一条测试证明不了任何东西。
	keyPath, _ := newTestSignerFile(t)
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成对照密钥失败: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-HH-Timestamp"), 10, 64)
		msg := hubclient.Message(r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body, ts, r.Header.Get("X-HH-Nonce"))
		sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-HH-Signature"))
		if !ed25519.Verify(otherPub, []byte(msg), sig) {
			writeAPIError(w, http.StatusUnauthorized, hubclient.CodeUnauthenticated, "签名校验失败")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer srv.Close()

	st := newTestHubService(t, srv.URL, keyPath).Status(context.Background())
	if st.Authenticated {
		t.Fatal("密钥不匹配时必须判定签名失败")
	}
}

// ============================================================
// 远端删除的 404 双重语义
// ============================================================

func TestDeleteRemoteSubscriptionTreatsMissingAsDeleted(t *testing.T) {
	// DELETE 404 + 列表 200 = 对方本来就没有这条，按「已删除」处理。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writeAPIError(w, http.StatusNotFound, hubclient.CodeNotFound, "订阅不存在")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer srv.Close()

	keyPath, _ := newTestSignerFile(t)
	already, err := newTestHubService(t, srv.URL, keyPath).DeleteRemoteSubscription(context.Background(), 7)
	if err != nil {
		t.Fatalf("不该返回错误: %v", err)
	}
	if !already {
		t.Fatal("对方没有这条时应报 already_deleted=true")
	}
}

func TestDeleteRemoteSubscriptionReportsModuleDisabled(t *testing.T) {
	// DELETE 404 + 列表 404 = RSS 模块关着。这不能当成删除成功：
	// 否则用户以为删掉了，实际什么都没发生。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, hubclient.CodeNotFound, "RSS 模块未启用")
	}))
	defer srv.Close()

	keyPath, _ := newTestSignerFile(t)
	already, err := newTestHubService(t, srv.URL, keyPath).DeleteRemoteSubscription(context.Background(), 7)
	if err == nil {
		t.Fatal("模块关闭时必须上报错误")
	}
	if already {
		t.Fatal("模块关闭时不能声称已删除")
	}
	if !hubclient.IsNotFound(err) {
		t.Fatalf("错误应保留 404 语义，实际 %v", err)
	}
}

// ============================================================
// 错误分类
// ============================================================

func TestClassifyHubErrorKeepsRequestIDVisible(t *testing.T) {
	// request_id 是唯一能和 Extension 侧日志对上的线索，
	// 丢掉它等于把排障入口关掉。
	err := &hubclient.APIError{
		StatusCode: http.StatusInternalServerError,
		Code:       hubclient.CodeInternal,
		Message:    "未预期错误",
		RequestID:  "511da52e177f79a6",
	}
	code, message := classifyHubError(err)

	if code != hubclient.CodeInternal {
		t.Fatalf("code 应为 %q，实际 %q", hubclient.CodeInternal, code)
	}
	if !strings.Contains(message, "511da52e177f79a6") {
		t.Fatalf("message 应带上 request_id，实际 %q", message)
	}
}

func TestClientReusesOneClientAcrossCalls(t *testing.T) {
	// 每次调用都重建客户端会让连接池失效，且每次都要重读私钥文件。
	// 配置没变时必须复用同一个实例。
	keyPath, _ := newTestSignerFile(t)
	svc := newTestHubService(t, "http://127.0.0.1:1/api/v1", keyPath)

	first, _, err := svc.client()
	if err != nil {
		t.Fatalf("首次构造失败: %v", err)
	}
	second, _, err := svc.client()
	if err != nil {
		t.Fatalf("再次取用失败: %v", err)
	}
	if first != second {
		t.Fatal("配置未变化时应复用同一个客户端")
	}
}

func TestClientRebuildsWhenBaseURLChanges(t *testing.T) {
	// 热更新改了地址之后必须换实例，否则请求会继续打到旧地址。
	keyPath, _ := newTestSignerFile(t)
	baseURL := "http://127.0.0.1:1/api/v1"
	svc := &ExtensionHubService{cfg: func() config.ExtensionHubConfig {
		return config.ExtensionHubConfig{Enabled: true, BaseURL: baseURL, PrivateKeyPath: keyPath, TimeoutSeconds: 5}
	}}

	first, _, err := svc.client()
	if err != nil {
		t.Fatalf("首次构造失败: %v", err)
	}

	baseURL = "http://127.0.0.1:2/api/v1"
	second, _, err := svc.client()
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if first == second {
		t.Fatal("base_url 变化后必须重建客户端")
	}
}

func TestStatusReportsEnabledButUnconfiguredWhenKeyIsMissing(t *testing.T) {
	// enabled=true 但私钥文件不存在时，enabled 必须仍为 true。
	// 若这里报 false，界面会显示「对接未启用」，用户会去翻开关，
	// 而真正要修的是键文件路径——错误提示比没有提示更糟。
	svc := &ExtensionHubService{
		cfg: func() config.ExtensionHubConfig {
			return config.ExtensionHubConfig{
				Enabled:        true,
				BaseURL:        "http://127.0.0.1:1/api/v1",
				PrivateKeyPath: filepath.Join(t.TempDir(), "missing.key"),
				TimeoutSeconds: 5,
			}
		},
	}

	st := svc.Status(context.Background())
	if !st.Enabled {
		t.Fatal("配置里 enabled=true 时 status.enabled 必须为 true")
	}
	if st.Configured {
		t.Fatal("私钥加载失败时 configured 必须为 false")
	}
	if st.ErrorCode != "config" {
		t.Fatalf("error_code 应为 config，实际 %q", st.ErrorCode)
	}
	if !strings.Contains(st.ErrorMessage, "missing.key") {
		t.Fatalf("错误信息应指出是哪个文件，实际 %q", st.ErrorMessage)
	}
}

// TestDeleteRemoteSubscriptionDoesNotClaimSuccessWhenProbeFails 锁住
// 「没确认 ≠ 已删除」。
//
// DELETE 回 404 意味着两种可能之一；区分它们靠一次列表探测。若探测本身
// 失败（429 限流是协议的正常回答，5xx 与连接中断同理），就无从判断到底
// 删掉没有。这时候报「已删除」是最坏的答案：用户不会再去重试一条他以为
// 已经消失的订阅，而它其实还在对端继续抓取。
func TestDeleteRemoteSubscriptionDoesNotClaimSuccessWhenProbeFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writeAPIError(w, http.StatusNotFound, hubclient.CodeNotFound, "订阅不存在")
			return
		}
		// 探测失败：模拟限流。
		w.Header().Set("Retry-After", "5")
		writeAPIError(w, http.StatusTooManyRequests, hubclient.CodeRateLimited, "限流")
	}))
	defer srv.Close()

	keyPath, _ := newTestSignerFile(t)
	already, err := newTestHubService(t, srv.URL, keyPath).DeleteRemoteSubscription(context.Background(), 7)

	if err == nil {
		t.Fatal("探测失败时必须上报错误，不能声称已删除")
	}
	if already {
		t.Fatal("探测失败时不能返回 already_deleted=true")
	}
}

// TestClientRereadsKeyWhenFileIsRotatedInPlace 确认同名覆盖私钥会生效。
//
// 轮换最常见的做法是 openssl genpkey -out 覆盖同名路径。只比较路径的
// 缓存会一直用旧身份签出 401，且不会有任何提示指向「该重读私钥」。
func TestClientRereadsKeyWhenFileIsRotatedInPlace(t *testing.T) {
	keyPath, firstPub := newTestSignerFile(t)
	svc := newTestHubService(t, "http://127.0.0.1:1/api/v1", keyPath)

	_, signer, err := svc.client()
	if err != nil {
		t.Fatalf("首次构造失败: %v", err)
	}
	if signer.PublicKeyBase64() != base64.StdEncoding.EncodeToString(firstPub) {
		t.Fatal("首次加载的公钥不对")
	}

	// 原地覆盖：路径不变，内容与 mtime 变化。
	// 用全 0x02 的种子生成一把确定不同的私钥。
	der, err := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, block, 0o600); err != nil {
		t.Fatalf("覆盖私钥失败: %v", err)
	}
	// 显式推进 mtime：部分文件系统的时间精度会吞掉同一秒内的两次写入。
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("调整 mtime 失败: %v", err)
	}

	_, rotated, err := svc.client()
	if err != nil {
		t.Fatalf("轮换后取用失败: %v", err)
	}
	if rotated.PublicKeyBase64() == base64.StdEncoding.EncodeToString(firstPub) {
		t.Fatal("同名覆盖私钥后必须重读，不能继续用旧身份")
	}
}
