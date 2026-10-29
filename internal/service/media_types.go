package service

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MediaCategory 媒体大类。与 model.MediaAsset.MediaType 的取值一致。
type MediaCategory string

const (
	CategoryImage MediaCategory = "image"
	CategoryVideo MediaCategory = "video"
	CategoryAudio MediaCategory = "audio"
	CategoryDoc   MediaCategory = "document"
)

// mediaFormat 描述一种受支持的上传格式。
//
// 单一事实源：扩展名、MIME、内容嗅探前缀、大类都定义在这里，
// 避免"白名单允许但 MIME 表没有"这类不一致（历史上 .svg 就踩过）。
type mediaFormat struct {
	MIME string
	// DetectPrefix 是 http.DetectContentType 结果应匹配的前缀。
	// 为空表示跳过内容嗅探（例如 svg 被嗅探为 text/xml，无法可靠区分）。
	DetectPrefix string
	Category     MediaCategory
}

// supportedFormats 是全部允许上传的扩展名 → 格式定义。
//
// 扩展时只需在此表加一行，上传校验、MIME 推断、类型归类会自动同步。
var supportedFormats = map[string]mediaFormat{
	// ── 图片 ──
	".jpg":  {MIME: "image/jpeg", DetectPrefix: "image/", Category: CategoryImage},
	".jpeg": {MIME: "image/jpeg", DetectPrefix: "image/", Category: CategoryImage},
	".png":  {MIME: "image/png", DetectPrefix: "image/", Category: CategoryImage},
	".gif":  {MIME: "image/gif", DetectPrefix: "image/", Category: CategoryImage},
	".webp": {MIME: "image/webp", DetectPrefix: "image/", Category: CategoryImage},
	".bmp":  {MIME: "image/bmp", DetectPrefix: "image/", Category: CategoryImage},
	".avif": {MIME: "image/avif", DetectPrefix: "image/", Category: CategoryImage},
	".ico":  {MIME: "image/x-icon", DetectPrefix: "image/", Category: CategoryImage},
	".tif":  {MIME: "image/tiff", DetectPrefix: "image/", Category: CategoryImage},
	".tiff": {MIME: "image/tiff", DetectPrefix: "image/", Category: CategoryImage},
	// SVG 会被嗅探为 text/xml，无法用前缀判断，故跳过内容校验。
	// 安全风险由响应头兜底：见 internal/middleware/security.go 的 nosniff + CSP。
	".svg": {MIME: "image/svg+xml", DetectPrefix: "", Category: CategoryImage},

	// ── 视频 ──
	".mp4":  {MIME: "video/mp4", DetectPrefix: "video/", Category: CategoryVideo},
	".m4v":  {MIME: "video/x-m4v", DetectPrefix: "video/", Category: CategoryVideo},
	".webm": {MIME: "video/webm", DetectPrefix: "video/", Category: CategoryVideo},
	".ogv":  {MIME: "video/ogg", DetectPrefix: "video/", Category: CategoryVideo},
	".mov":  {MIME: "video/quicktime", DetectPrefix: "video/", Category: CategoryVideo},
	".mkv":  {MIME: "video/x-matroska", DetectPrefix: "", Category: CategoryVideo},
	".avi":  {MIME: "video/x-msvideo", DetectPrefix: "", Category: CategoryVideo},

	// ── 音频 ──
	".mp3":  {MIME: "audio/mpeg", DetectPrefix: "audio/", Category: CategoryAudio},
	".m4a":  {MIME: "audio/mp4", DetectPrefix: "audio/", Category: CategoryAudio},
	".aac":  {MIME: "audio/aac", DetectPrefix: "audio/", Category: CategoryAudio},
	".ogg":  {MIME: "audio/ogg", DetectPrefix: "audio/", Category: CategoryAudio},
	".oga":  {MIME: "audio/ogg", DetectPrefix: "audio/", Category: CategoryAudio},
	".opus": {MIME: "audio/opus", DetectPrefix: "audio/", Category: CategoryAudio},
	".wav":  {MIME: "audio/wav", DetectPrefix: "audio/", Category: CategoryAudio},
	".flac": {MIME: "audio/flac", DetectPrefix: "", Category: CategoryAudio},

	// ── 文档 ──
	".pdf": {MIME: "application/pdf", DetectPrefix: "application/pdf", Category: CategoryDoc},
}

// SupportedExtensions 返回全部受支持的扩展名（含点号，已排序由调用方决定）。
//
// 供前端提示与文档生成使用，避免文档与代码脱节。
func SupportedExtensions() []string {
	out := make([]string, 0, len(supportedFormats))
	for ext := range supportedFormats {
		out = append(out, ext)
	}
	return out
}

// normalizeExt 统一扩展名格式：小写且带前导点。
func normalizeExt(filename string) string {
	return strings.ToLower(filepath.Ext(filename))
}

// lookupFormat 查找扩展名对应的格式定义。
func lookupFormat(ext string) (mediaFormat, bool) {
	f, ok := supportedFormats[ext]
	return f, ok
}

// getMediaType 根据扩展名返回媒体大类。
func getMediaType(ext string) (string, bool) {
	f, ok := lookupFormat(ext)
	if !ok {
		return "", false
	}
	return string(f.Category), true
}

// detectMimeType 根据扩展名推断 MIME 类型。
//
// 表里没有的扩展名回落到 octet-stream；上传入口已用 supportedFormats 拦截，
// 该回落只用于历史数据。
func detectMimeType(ext string) string {
	if f, ok := lookupFormat(ext); ok {
		return f.MIME
	}
	return "application/octet-stream"
}

// validateFileContent 读取文件头，用 http.DetectContentType 检测真实类型，
// 与扩展名登记的类别对照，防止改扩展名伪装。
//
// 对无法可靠嗅探的格式（svg/mkv/avi/flac）跳过内容校验 —— 这些格式的
// 嗅探结果是 text/xml 或 application/octet-stream，无法用于判定。
func validateFileContent(path, ext string) error {
	f, ok := lookupFormat(ext)
	if !ok {
		return ErrInvalidFileType
	}
	if f.DetectPrefix == "" {
		return nil
	}

	data, err := readFileHead(path, 512)
	if err != nil {
		return fmt.Errorf("读取文件进行内容校验失败: %w", err)
	}

	detected := detectContentType(data)
	if !strings.HasPrefix(detected, f.DetectPrefix) {
		return fmt.Errorf("文件内容与扩展名不匹配：扩展名 %s 期望 %s*，检测到 %s",
			ext, f.DetectPrefix, detected)
	}
	return nil
}

// readFileHead 读取文件开头最多 n 字节。
//
// 原实现用 os.ReadFile 把整个文件读进内存（最大 50MB）只为嗅探 MIME，
// 这里只读文件头，避免上传大视频时的内存峰值。
func readFileHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:read], nil
}

// detectContentType 是 http.DetectContentType 的薄封装，便于测试替换。
func detectContentType(data []byte) string {
	return http.DetectContentType(data)
}

// imageDimensions 读取图片的像素尺寸。
//
// 只对 Go 标准库内置解码器（jpeg/png/gif）生效；webp/avif/svg 等需要
// 第三方解码器，为免引入依赖直接返回 (0, 0)，前端会省略尺寸显示。
//
// 返回错误时调用方应忽略：尺寸只用于展示，不应导致上传失败。
func imageDimensions(path string) (width, height int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
