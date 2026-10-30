package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestImageFilenameIsUnique 锁住"避免重复"的文件名层保障。
//
// 同一份内容在不同时刻生成的文件名必须不同（时间戳/随机段），
// 但同时必须保留内容哈希后级，便于排障时反查。
//
// 注意这里验证的是**文件名**不重复，而不是内容身份：内容身份由
// RSSImage.CacheKey 保证，两者职责分开。
func TestImageFilenameIsUnique(t *testing.T) {
	const hash = "51dfc9141a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5"

	a := imageFilename(hash, ".png", time.Unix(1700000000, 0))
	b := imageFilename(hash, ".png", time.Unix(1700000000, 0))

	if a == b {
		t.Fatalf("同一内容两次生成相同文件名 %q，随机段失效", a)
	}
}

// TestImageFilenameShape 确认文件名由三段组成且扩展名正确。
func TestImageFilenameShape(t *testing.T) {
	const hash = "51dfc9141a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5"
	const ext = ".webp"

	name := imageFilename(hash, ext, time.Unix(1700000000, 0))

	if !strings.HasPrefix(name, "rss_") {
		t.Errorf("缺少 rss_ 前缀: %q", name)
	}
	if filepath.Ext(name) != ext {
		t.Errorf("扩展名 = %q, want %q", filepath.Ext(name), ext)
	}
	if !strings.HasSuffix(strings.TrimSuffix(name, ext), hash[:8]) {
		t.Errorf("文件名应包含内容哈希前8位 %q: %q", hash[:8], name)
	}
	// 三段：rss_<ts>_<rand>_<hash8>
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "rss_"), ext), "_")
	if len(parts) != 3 {
		t.Errorf("应为 时间戳_随机_哈希 三段，实际 %d 段: %q", len(parts), name)
	}
	if len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) != 8 {
		t.Errorf("三段内容不符合预期: %v", parts)
	}
}

// TestImageFilenameHandlesEmptyHash 确认空哈希不会 panic 或产生空段。
func TestImageFilenameHandlesEmptyHash(t *testing.T) {
	name := imageFilename("", ".jpg", time.Now())
	if strings.Contains(name, "__") {
		t.Errorf("空哈希产生了空段: %q", name)
	}
	if filepath.Ext(name) != ".jpg" {
		t.Errorf("扩展名不对: %q", name)
	}
}

// TestImageFilenameIsPathSafe 确认生成的名字不含路径分隔符，
// 避免被当作路径使用（Adopt 对传入文件名有这个校验）。
func TestImageFilenameIsPathSafe(t *testing.T) {
	name := imageFilename("abcdef1234567890", ".png", time.Now())
	if filepath.Base(name) != name {
		t.Errorf("文件名包含路径成分: %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		t.Errorf("文件名含路径分隔符: %q", name)
	}
}

// TestNormalizeSHA256 确认只接受合法的 64 位十六进制内容哈希。
//
// 这个函数守着唯一键：把空串、md5、带前缀的值写进 CacheKey，
// 会让同一份内容产生多个身份，去重直接失效。
func TestNormalizeSHA256(t *testing.T) {
	valid := strings.Repeat("a1", 32) // 64 字符
	cases := []struct {
		name  string
		in    string
		want  string
		valid bool
	}{
		{"合法小写", valid, valid, true},
		{"合法大写被规范化", strings.ToUpper(valid), valid, true},
		{"前后空格被裁剪", "  " + valid + "\n", valid, true},
		{"空串", "", "", false},
		{"md5 长度不符", strings.Repeat("a", 32), "", false},
		{"含非十六进制字符", strings.Repeat("z", 64), "", false},
		{"带前缀", "sha256:" + valid, "", false},
	}
	for _, c := range cases {
		got := normalizeSHA256(c.in)
		if got != c.want {
			t.Errorf("%s: normalizeSHA256(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if c.valid && got == "" {
			t.Errorf("%s: 合法输入被判为非法", c.name)
		}
	}
}

// TestFileSHA256MatchesContent 确认文件哈希与内容一致，且不同内容不同哈希。
func TestFileSHA256MatchesContent(t *testing.T) {
	dir := t.TempDir()

	p1 := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(p1, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(p2, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1 := fileSHA256(p1)
	h2 := fileSHA256(p2)

	if h1 == "" || h2 == "" {
		t.Fatal("文件哈希不应为空")
	}
	if len(h1) != 64 {
		t.Errorf("哈希长度 = %d, want 64", len(h1))
	}
	if h1 == h2 {
		t.Error("不同内容得到相同哈希")
	}
	if fileSHA256(filepath.Join(dir, "missing.bin")) != "" {
		t.Error("不存在的文件应返回空串")
	}
}

// TestLookupByContentWithoutDB 确认没有数据库时不会 panic 且返回未命中。
//
// 无 db 是合法运行形态（测试与 extension 预下载场景），此时去重降级为
// 进程内 URL 映射，而内容级去重不可用。
func TestLookupByContentWithoutDB(t *testing.T) {
	c := &LocalImageCache{dir: t.TempDir()}
	if _, ok := c.lookupByContent("deadbeef"); ok {
		t.Error("无数据库时不应命中内容缓存")
	}
}

// TestSha256HexDistinguishesURLs 确认 URL 身份与内容身份使用同一个函数
// 但输入不同，结果不同。
func TestSha256HexDistinguishesURLs(t *testing.T) {
	a := sha256Hex("https://example.com/a.png")
	b := sha256Hex("https://example.com/b.png")

	if a == b {
		t.Fatal("不同 URL 得到相同哈希")
	}
	if len(a) != 64 {
		t.Errorf("URL 哈希长度 = %d, want 64", len(a))
	}
	if a != sha256Hex("https://example.com/a.png") {
		t.Error("同一 URL 两次哈希结果不一致")
	}
}
