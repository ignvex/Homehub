package hubclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件锁定协议里最容易写错的那一层：待签消息的确切字节。
//
// 签名算法本身是 Ed25519，不会出错；出错的是"签了什么"。协议的七行
// 消息里，PATH 与 QUERY 的编码形态、空 body 的摘要、行尾是否多一个
// 换行，任何一处不一致都会让请求稳定地得到 401 —— 而且看起来像是
// 密钥配错了。

// TestSignMatchesProtocolVector 复现协议文档 §3.6 的自检向量。
//
// 这组向量是外部给定的（固定种子、固定时间戳、固定 nonce），不是从
// 本实现反推出来的，因此能独立验证实现是否符合别人的约定。
func TestSignMatchesProtocolVector(t *testing.T) {
	seed := bytes.Repeat([]byte{1}, ed25519.SeedSize)
	signer, err := NewSigner(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("构造签名器失败: %v", err)
	}

	body := []byte(`{"url":"https://example.com/feed.xml","interval":"30m"}`)

	const (
		wantDigest    = "fae5596b35f13d7a967e1ce18e29a078c2a4c25c432b4445f6d1b146db1129e5"
		wantPublicKey = "iojj3XQJ8ZX9UtstPLpdcspnCb8dlBIb83SIAbQPb1w="
		wantSignature = "/kzfFqg4awpnAl+6XTpVhYaX3zhMw+qkubelssgiEO8sRRDH1l2S/rrKvDohfpVzmvMFooTpufw4ulRCPRZYCA=="
	)

	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != wantDigest {
		t.Fatalf("body 摘要 = %s，期望 %s", got, wantDigest)
	}

	if got := signer.PublicKeyBase64(); got != wantPublicKey {
		t.Fatalf("公钥 = %s，期望 %s", got, wantPublicKey)
	}

	got := signer.Sign("POST", "/api/v1/hub/subscriptions", "dry=1", body, 1793251200, "test-nonce-0001")
	if got != wantSignature {
		t.Fatalf("签名 = %s\n期望 = %s", got, wantSignature)
	}
}

// TestMessageIsExactlySevenLines 固定待签消息的字面形态。
//
// 逐字节比对而不是"包含各段"：一旦有人在末尾加了 \n 或把 METHOD 转成
// 小写，签名依然"看起来合理"，只有 Extension 会拒绝。
func TestMessageIsExactlySevenLines(t *testing.T) {
	body := []byte(`{"url":"https://example.com/feed.xml","interval":"30m"}`)
	got := Message("POST", "/api/v1/hub/subscriptions", "dry=1", body, 1793251200, "test-nonce-0001")

	want := strings.Join([]string{
		"homehubext/req/v1",
		"POST",
		"/api/v1/hub/subscriptions",
		"dry=1",
		"fae5596b35f13d7a967e1ce18e29a078c2a4c25c432b4445f6d1b146db1129e5",
		"1793251200",
		"test-nonce-0001",
	}, "\n")

	if got != want {
		t.Fatalf("消息不符\n实际: %q\n期望: %q", got, want)
	}
	if strings.HasSuffix(got, "\n") {
		t.Error("消息末尾不应有换行")
	}
	if n := strings.Count(got, "\n"); n != 6 {
		t.Errorf("换行数 = %d，七行消息应为 6", n)
	}
}

// TestEmptyBodyUsesEmptyStringDigest 确认空 body 也参与摘要。
//
// 这是协议里明确点名的坑：把空 body 的摘要当成空字符串，而不是
// SHA-256("")，会让所有不带 body 的请求（GET、DELETE）全部 401。
func TestEmptyBodyUsesEmptyStringDigest(t *testing.T) {
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	for _, body := range [][]byte{nil, {}} {
		msg := Message("DELETE", "/api/v1/hub/subscriptions/7", "", body, 1793251200, "n1")
		lines := strings.Split(msg, "\n")
		if len(lines) != 7 {
			t.Fatalf("行数 = %d，期望 7", len(lines))
		}
		if lines[4] != emptyDigest {
			t.Errorf("空 body 摘要 = %q，期望 %q", lines[4], emptyDigest)
		}
		if lines[3] != "" {
			t.Errorf("无 query 时第 4 行应为空，实际 %q", lines[3])
		}
	}
}

// TestMessageNormalizesMethod 确认方法统一成大写。
func TestMessageNormalizesMethod(t *testing.T) {
	upper := Message("post", "/p", "", nil, 1, "n")
	lower := Message("POST", "/p", "", nil, 1, "n")
	if upper != lower {
		t.Errorf("大写与小写方法应产生相同消息\n%q\n%q", upper, lower)
	}
}

// TestMessageKeepsQueryOrder 确认 query 不被排序或重编码。
//
// 协议要求原样使用 RawQuery。如果实现顺手做了排序，构造时和发送时
// 各排一次看起来一致，但与调用方给的顺序不符时会与 Extension 不一致。
func TestMessageKeepsQueryOrder(t *testing.T) {
	msg := Message("GET", "/p", "z=1&a=2&m=3", nil, 1, "n")
	lines := strings.Split(msg, "\n")
	if lines[3] != "z=1&a=2&m=3" {
		t.Errorf("query 被改写为 %q", lines[3])
	}
}

func TestNewSignerRejectsWrongLength(t *testing.T) {
	if _, err := NewSigner(make([]byte, 16)); err == nil {
		t.Error("长度错误的私钥应被拒绝")
	}
}

func TestNewNonceIsRandomHex(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		nonce, err := NewNonce()
		if err != nil {
			t.Fatalf("生成 nonce 失败: %v", err)
		}
		if len(nonce) != NonceBytes*2 {
			t.Fatalf("nonce 长度 = %d，期望 %d", len(nonce), NonceBytes*2)
		}
		if _, err := hex.DecodeString(nonce); err != nil {
			t.Fatalf("nonce 不是合法 hex: %v", err)
		}
		if _, dup := seen[nonce]; dup {
			t.Fatalf("nonce 重复: %s", nonce)
		}
		seen[nonce] = struct{}{}
	}
}

// ---------- LoadSigner ----------

func TestLoadSignerAcceptsPKCS8PEM(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	key := ed25519.NewKeyFromSeed(seed)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("序列化 PKCS#8 失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "hub.key")
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("写入私钥失败: %v", err)
	}

	signer, err := LoadSigner(path)
	if err != nil {
		t.Fatalf("加载 PKCS#8 私钥失败: %v", err)
	}
	if !bytes.Equal(signer.PublicKey(), key.Public().(ed25519.PublicKey)) {
		t.Error("加载到的公钥与原始私钥不匹配")
	}
}

func TestLoadSignerAcceptsRawSeedAndBase64(t *testing.T) {
	seed := bytes.Repeat([]byte{9}, ed25519.SeedSize)
	want := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	dir := t.TempDir()

	cases := map[string][]byte{
		"raw.key":    seed,
		"base64.key": []byte(base64.StdEncoding.EncodeToString(seed)),
	}
	for name, content := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("写入 %s 失败: %v", name, err)
		}
		signer, err := LoadSigner(path)
		if err != nil {
			t.Fatalf("%s: 加载失败: %v", name, err)
		}
		if !bytes.Equal(signer.PublicKey(), want) {
			t.Errorf("%s: 公钥不匹配", name)
		}
	}
}

// TestLoadSignerRejectsPublicKey 确认把公钥当私钥配会立刻报错。
//
// 否则签名永远验不过，表现为稳定的 401，排查方向会被引到时钟和 nonce 上。
func TestLoadSignerRejectsPublicKey(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, ed25519.SeedSize))
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatalf("序列化公钥失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pub.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("写入公钥失败: %v", err)
	}

	_, err = LoadSigner(path)
	if err == nil {
		t.Fatal("公钥被当作私钥接受了")
	}
	if !strings.Contains(err.Error(), "公钥") {
		t.Errorf("错误信息应点明这是公钥，实际: %v", err)
	}
}

func TestLoadSignerMissingFile(t *testing.T) {
	if _, err := LoadSigner(filepath.Join(t.TempDir(), "nope.key")); err == nil {
		t.Error("文件不存在时应报错")
	}
}
