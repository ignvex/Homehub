package service

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestSupportedFormatsConsistency 确认格式表自身一致。
//
// 历史上 .svg 出现在 MIME 表却不在上传白名单里，导致"能识别但传不上"。
// 现在只有一张表，这条测试守住它的一致性。
func TestSupportedFormatsConsistency(t *testing.T) {
	if len(supportedFormats) == 0 {
		t.Fatal("supportedFormats 为空")
	}

	for ext, f := range supportedFormats {
		if ext == "" || ext[0] != '.' {
			t.Errorf("扩展名 %q 缺少前导点", ext)
		}
		if ext != normalizeExt("file"+ext) {
			t.Errorf("扩展名 %q 未规范化为小写", ext)
		}
		if f.MIME == "" {
			t.Errorf("%s 缺少 MIME", ext)
		}
		switch f.Category {
		case CategoryImage, CategoryVideo, CategoryAudio, CategoryDoc:
		default:
			t.Errorf("%s 的类型 %q 不是已知类别", ext, f.Category)
		}
		// MIME 大类应与登记类别一致（x-icon/tiff 等均以 image/ 开头）
		if f.Category != CategoryDoc && f.MIME != "" {
			prefix := string(f.Category) + "/"
			if !hasAnyPrefix(f.MIME, prefix) {
				t.Errorf("%s 的 MIME %q 与类别 %q 不匹配", ext, f.MIME, f.Category)
			}
		}
	}
}

// hasAnyPrefix 处理 image/x-icon、video/x-m4v 这类 MIME。
func hasAnyPrefix(mime, prefix string) bool {
	if len(mime) >= len(prefix) && mime[:len(prefix)] == prefix {
		return true
	}
	// x- 前缀的变体
	alt := prefix[:len(prefix)-1] + "/x-"
	return len(mime) >= len(alt) && mime[:len(alt)] == alt
}

// TestGetMediaTypeCategories 确认各类文件被归到正确大类。
func TestGetMediaTypeCategories(t *testing.T) {
	cases := map[string]string{
		".jpg":  "image",
		".svg":  "image",
		".avif": "image",
		".mp4":  "video",
		".mkv":  "video",
		".mp3":  "audio",
		".flac": "audio",
		".pdf":  "document",
	}
	for ext, want := range cases {
		got, ok := getMediaType(ext)
		if !ok {
			t.Errorf("%s 未被识别为受支持格式", ext)
			continue
		}
		if got != want {
			t.Errorf("%s 类型 = %q，期望 %q", ext, got, want)
		}
	}

	if _, ok := getMediaType(".exe"); ok {
		t.Error(".exe 不应被接受")
	}
	if _, ok := getMediaType(""); ok {
		t.Error("空扩展名不应被接受")
	}
}

// TestDetectMimeTypeCoversAllFormats 确认每个受支持扩展名都有 MIME。
func TestDetectMimeTypeCoversAllFormats(t *testing.T) {
	for ext, f := range supportedFormats {
		if got := detectMimeType(ext); got != f.MIME {
			t.Errorf("%s MIME = %q，期望 %q", ext, got, f.MIME)
		}
	}
	if got := detectMimeType(".unknown"); got != "application/octet-stream" {
		t.Errorf("未知扩展名应回落 octet-stream，实际 %q", got)
	}
}

// TestValidateFileContentRejectsSpoof 确认改名伪装会被拦下。
func TestValidateFileContentRejectsSpoof(t *testing.T) {
	dir := t.TempDir()

	// 内容是 PNG，扩展名是 .mp4
	pngPath := filepath.Join(dir, "fake.mp4")
	writePNG(t, pngPath, 4, 4, nil)

	if err := validateFileContent(pngPath, ".mp4"); err == nil {
		t.Error("PNG 内容伪装成 .mp4 应被拒绝")
	}
}

// TestValidateFileContentAcceptsRealImage 确认正常图片通过。
func TestValidateFileContentAcceptsRealImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "real.png")
	writePNG(t, p, 3, 2, nil)

	if err := validateFileContent(p, ".png"); err != nil {
		t.Errorf("正常 PNG 被拒绝: %v", err)
	}
}

// TestValidateFileContentSkipsUnsniffable 确认无法嗅探的格式跳过内容校验。
func TestValidateFileContentSkipsUnsniffable(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct{ ext, body string }{
		{".svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`},
		{".mkv", "\x1a\x45\xdf\xa3"},
		{".flac", "fLaC"},
	} {
		p := filepath.Join(dir, "file"+tc.ext)
		if err := os.WriteFile(p, []byte(tc.body), 0644); err != nil {
			t.Fatal(err)
		}
		if err := validateFileContent(p, tc.ext); err != nil {
			t.Errorf("%s 应跳过内容校验，却被拒绝: %v", tc.ext, err)
		}
	}
}

// TestImageDimensions 确认能读出 PNG 尺寸，且对非图片返回 0。
func TestImageDimensions(t *testing.T) {
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "d.png")
	writePNG(t, pngPath, 7, 5, nil)

	w, h := imageDimensions(pngPath)
	if w != 7 || h != 5 {
		t.Errorf("尺寸 = %dx%d，期望 7x5", w, h)
	}

	notImage := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(notImage, []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}
	if w, h := imageDimensions(notImage); w != 0 || h != 0 {
		t.Errorf("非图片应返回 0x0，实际 %dx%d", w, h)
	}
}

// writePNG 生成指定尺寸的 PNG 文件。
func writePNG(t *testing.T, path string, w, h int, fill []byte) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if fill != nil {
		copy(img.Pix, fill)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码 PNG 失败: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("写入 PNG 失败: %v", err)
	}
}

// TestBackgroundKindsExcludeNonVisual 确认背景只接受图片与视频。
//
// 音频与 PDF 可以上传和展示，但浏览器无法把音频渲染成背景层、
// PDF 只能内联打开。若混入背景配置，首页会渲染出错位画面。
func TestBackgroundKindsExcludeNonVisual(t *testing.T) {
	allowed := map[string]bool{}
	for _, k := range BackgroundKinds {
		allowed[k] = true
	}

	if !allowed["image"] || !allowed["video"] {
		t.Errorf("图片与视频必须可作背景，实际: %v", BackgroundKinds)
	}
	for _, k := range []string{"audio", "document"} {
		if allowed[k] {
			t.Errorf("%s 不应出现在背景白名单中", k)
		}
	}
}
