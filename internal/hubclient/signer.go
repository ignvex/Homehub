// Package hubclient 实现 HomeHub 作为宿主，调用 HomeHub Extension 的
// /api/v1/hub/* 机器通道。
//
// 这条通道只认请求签名，不认 Bearer Token：宿主持有一把 Ed25519 私钥，
// Extension 侧保存对应公钥，验签通过即等于"请求方持有私钥"。身份不是
// 某个字段，而是签名本身 —— 所以本包不提供任何 Token 相关的选项。
//
// 签名消息是七行、以 \n 连接、无尾随换行：
//
//	homehubext/req/v1
//	<METHOD>
//	<PATH>          百分号编码后的路径（URL.EscapedPath）
//	<QUERY>         原始 query 串（RawQuery），不排序不重编码
//	<hex(sha256(body))>
//	<timestamp>     十进制 Unix 秒
//	<nonce>         每次请求唯一
//
// 空 body 也要参与摘要，空串的 SHA-256 是 e3b0c442…，不是空字符串。
// Ed25519 内部已含哈希，不要再对消息做一次 SHA-256。
package hubclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// SignDomain 是签名消息的前缀。协议发生不兼容变更时才会升到 v2，
// 届时 Extension 会提供并行支持窗口 —— 所以这里不要改成"当前版本"。
const SignDomain = "homehubext/req/v1"

// NonceBytes 是随机 nonce 的字节数，协议建议 16。
const NonceBytes = 16

// Signer 持有宿主私钥，为每次请求生成签名。
//
// 私钥只在构造时读取一次，之后不可变，因此可以并发使用。
type Signer struct {
	key ed25519.PrivateKey
}

// NewSigner 用给定的 Ed25519 私钥构造签名器。
func NewSigner(key ed25519.PrivateKey) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("hubclient: Ed25519 私钥长度 %d，期望 %d", len(key), ed25519.PrivateKeySize)
	}
	return &Signer{key: key}, nil
}

// LoadSigner 从文件加载 Ed25519 私钥。
//
// 支持三种形态，按部署脚本的常见程度排序：
//   - PKCS#8 PEM（`openssl genpkey -algorithm ed25519` 的输出）
//   - 裸 32 字节种子或 64 字节私钥的二进制文件
//   - 上述内容的 base64 文本
//
// 不接受 PKIX 公钥 PEM：把公钥当私钥配上去会静默签出永远验不过的请求，
// 不如在这里直接报错。
func LoadSigner(path string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hubclient: 读取私钥失败: %w", err)
	}
	signer, err := LoadSignerFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("hubclient: %s: %w", path, err)
	}
	return signer, nil
}

// LoadSignerFromBytes 从内存里的内容构造签名器，接受与 LoadSigner
// 完全相同的三种形态。
//
// 需要它是因为私钥不总在文件里：存进数据库之后，取出来的是一段字节，
// 再写临时文件去读只是多一次不必要的落盘（而落盘正是要避开的）。
func LoadSignerFromBytes(raw []byte) (*Signer, error) {
	key, err := parsePrivateKey(raw)
	if err != nil {
		return nil, err
	}
	return NewSigner(key)
}

// ParsePublicKeyPEM 把 PKIX PEM 形式的公钥解析成 base64 字符串。
//
// 只用于展示与核对（部署时要把公钥贴到 Extension 侧），不参与验签。
func ParsePublicKeyPEM(raw []byte) (string, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", fmt.Errorf("不是 PEM 格式")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("解析公钥失败: %w", err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("公钥不是 Ed25519（实际 %T）", parsed)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// parsePrivateKey 把 PEM / 原始字节 / base64 文本解析成 Ed25519 私钥。
func parsePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("私钥文件为空")
	}

	if block, _ := pem.Decode(raw); block != nil {
		switch block.Type {
		case "PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("解析 PKCS#8 私钥失败: %w", err)
			}
			key, ok := parsed.(ed25519.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("私钥不是 Ed25519（实际 %T）", parsed)
			}
			return key, nil
		case "PUBLIC KEY":
			return nil, fmt.Errorf("这是公钥不是私钥，应配置 PKCS#8 私钥")
		}
		// 其他类型（如 ED25519 PRIVATE KEY）按裸字节处理。
		return keyFromBytes(block.Bytes)
	}

	trimmed := strings.TrimSpace(string(raw))
	if isBase64Text(trimmed) {
		decoded, err := base64.StdEncoding.DecodeString(trimmed)
		if err == nil {
			if key, kerr := keyFromBytes(decoded); kerr == nil {
				return key, nil
			}
		}
	}
	return keyFromBytes(raw)
}

// isBase64Text 判断内容是否像 base64：长度是 4 的倍数且只含字母数字与 +/=。
func isBase64Text(s string) bool {
	if s == "" || len(s)%4 != 0 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/', r == '=':
		default:
			return false
		}
	}
	return true
}

// keyFromBytes 接受 32 字节种子或 64 字节完整私钥。
func keyFromBytes(b []byte) (ed25519.PrivateKey, error) {
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		// 复制一份，避免调用方继续持有底层数组。
		key := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
		copy(key, b)
		return key, nil
	default:
		return nil, fmt.Errorf("私钥长度 %d 字节，期望 %d（种子）或 %d（私钥）",
			len(b), ed25519.SeedSize, ed25519.PrivateKeySize)
	}
}

// PublicKey 返回对应公钥，便于部署时核对 Extension 侧配置。
func (s *Signer) PublicKey() ed25519.PublicKey {
	pub, ok := s.key.Public().(ed25519.PublicKey)
	if !ok {
		return nil
	}
	return pub
}

// PublicKeyBase64 按协议给出的形式（标准 base64）输出公钥。
//
// 注意这是**裸 32 字节**公钥的 base64，不是 PKIX DER 的 base64。两者
// 相差 12 字节的 ASN.1 头（302a300506032b6570032100），粘贴给对方时
// 选错格式会被当成无效公钥。要给人复制用 PublicKeyPEM 更稳。
func (s *Signer) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(s.PublicKey())
}

// PublicKeyPEM 输出 PKIX PEM 形式的公钥（带 BEGIN/END 头尾）。
//
// 部署时推荐用这个而不是裸 base64：PEM 自带类型标记，不会与「DER 的
// base64」混淆，也正好对应 openssl pkey -pubout 的输出。
func (s *Signer) PublicKeyPEM() (string, error) {
	der, err := x509.MarshalPKIXPublicKey(s.PublicKey())
	if err != nil {
		return "", fmt.Errorf("编码公钥失败: %w", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if block == nil {
		return "", fmt.Errorf("生成公钥 PEM 失败")
	}
	return string(block), nil
}

// Sign 生成 base64 编码的签名。
//
// escapedPath 必须是最终发出去的百分号编码路径（Go 的 URL.EscapedPath），
// rawQuery 必须是最终发出去的原始 query 串。两者与请求不一致时，
// Extension 会按字节比较并拒绝 —— 这正是签名要保证的。
func (s *Signer) Sign(method, escapedPath, rawQuery string, body []byte, timestamp int64, nonce string) string {
	msg := Message(method, escapedPath, rawQuery, body, timestamp, nonce)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.key, []byte(msg)))
}

// Message 构造待签消息。单独导出是为了让测试与排查能直接比对字节。
func Message(method, escapedPath, rawQuery string, body []byte, timestamp int64, nonce string) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		SignDomain,
		strings.ToUpper(method),
		escapedPath,
		rawQuery,
		hex.EncodeToString(sum[:]),
		strconv.FormatInt(timestamp, 10),
		nonce,
	}, "\n")
}

// NewNonce 生成 16 字节随机数并转成小写 hex。
//
// 每次都必须重来：Extension 对 nonce 去重，重放同一个值会得到 401。
func NewNonce() (string, error) {
	buf := make([]byte, NonceBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("hubclient: 生成 nonce 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Now 返回签名用的时间戳，抽成变量方便测试替换。
var Now = func() time.Time { return time.Now() }

// SignRaw 对任意字节做一次裸 Ed25519 签名。
//
// 不用于协议（协议走 Sign 的七行消息），只给「私钥自检」这类内部用途：
// 能解析不代表能用，签一次再验一次才能确认内容没被截断。
func (s *Signer) SignRaw(msg []byte) []byte {
	return ed25519.Sign(s.key, msg)
}

// PrivateKeyPEM 把私钥规范成 PKCS#8 PEM。
//
// 入库前统一成一种格式：输入的三种形态（PEM / 裸种子 / base64 文本）
// 都归一到 PEM，回读时不用再猜是哪种。
func (s *Signer) PrivateKeyPEM() (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(s.key)
	if err != nil {
		return "", fmt.Errorf("编码私钥失败: %w", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if block == nil {
		return "", fmt.Errorf("生成 PEM 失败")
	}
	return string(block), nil
}
