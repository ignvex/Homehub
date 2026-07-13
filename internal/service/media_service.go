package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// 上传相关常量。
const (
	uploadDir     = "web/static/uploads"
	maxUploadSize = 50 << 20 // 50 MB
)

var (
	allowedImageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true}
	allowedVideoExt = map[string]bool{".mp4": true, ".webm": true, ".ogv": true, ".mov": true}
)

var (
	// ErrFileTooLarge 文件超过大小限制。
	ErrFileTooLarge = errors.New("文件大小不能超过 50MB")
	// ErrInvalidFileType 不支持的文件类型。
	ErrInvalidFileType = errors.New("不支持的文件类型，仅支持常见图片和视频格式")
	// ErrAssetNotFound 媒体资源不存在。
	ErrAssetNotFound = errors.New("媒体资源不存在")
)

// MediaService 媒体资源业务服务。
type MediaService struct {
	db *gorm.DB
}

// NewMediaService 创建 MediaService 实例。
func NewMediaService(db *gorm.DB) *MediaService {
	return &MediaService{db: db}
}

// List 返回所有媒体资源，按创建时间倒序排列。
func (s *MediaService) List() ([]model.MediaAsset, error) {
	var assets []model.MediaAsset
	if err := s.db.Order("created_at DESC").Find(&assets).Error; err != nil {
		return nil, fmt.Errorf("查询媒体资源列表失败: %w", err)
	}
	return assets, nil
}

// GetByID 根据 ID 获取单个媒体资源。
func (s *MediaService) GetByID(id uint64) (*model.MediaAsset, error) {
	var asset model.MediaAsset
	if err := s.db.First(&asset, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssetNotFound
		}
		return nil, fmt.Errorf("查询媒体资源失败: %w", err)
	}
	return &asset, nil
}

// ListByIDs 按传入 ID 的顺序查询媒体资源，不存在的资源会被忽略。
// 使用单次 WHERE IN 查询替代循环中的 N 次 First()，避免 N+1 问题。
func (s *MediaService) ListByIDs(ids []uint64) ([]model.MediaAsset, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var assets []model.MediaAsset
	if err := s.db.Where("id IN ?", ids).Find(&assets).Error; err != nil {
		return nil, fmt.Errorf("查询媒体资源失败: %w", err)
	}

	// 按输入顺序重排
	idIndex := make(map[uint64]int, len(ids))
	for i, id := range ids {
		idIndex[id] = i
	}

	ordered := make([]model.MediaAsset, len(ids))
	count := 0
	for i := range assets {
		idx, ok := idIndex[assets[i].ID]
		if !ok {
			continue
		}
		ordered[idx] = assets[i]
		count++
	}

	return ordered[:count], nil
}

// Upload 保存上传文件并创建数据库记录。
//
// 流程：
//  1. 校验文件大小和 MIME 类型
//  2. 生成 UUID 文件名
//  3. 创建 uploads 目录（如不存在）
//  4. 写入文件
//  5. 创建数据库记录
func (s *MediaService) Upload(file *multipart.FileHeader) (*model.MediaAsset, error) {
	// 1. 校验文件大小
	if file.Size > maxUploadSize {
		return nil, ErrFileTooLarge
	}

	// 2. 校验扩展名（拒绝空扩展名，防止绕过检查）
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext == "" {
		return nil, ErrInvalidFileType
	}
	mediaType, ok := getMediaType(ext)
	if !ok {
		return nil, ErrInvalidFileType
	}

	// 3. 生成唯一文件名
	uuid, err := generateUUID()
	if err != nil {
		return nil, fmt.Errorf("生成文件名失败: %w", err)
	}
	filename := uuid + ext
	relativePath := filepath.Join("uploads", filename)
	destPath := filepath.Join("web", "static", relativePath)

	// 4. 确保目录存在
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return nil, fmt.Errorf("创建上传目录失败: %w", err)
	}

	// 5. 保存文件
	src, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("打开上传文件失败: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		// 写入失败则删除已创建的目标文件
		os.Remove(destPath)
		return nil, fmt.Errorf("写入文件失败: %w", err)
	}
	dst.Close()

	// 5.5 服务端 MIME 校验（读取文件前 512 字节检测真实内容类型，防止扩展名伪装）
	if err := validateFileContent(destPath, ext); err != nil {
		os.Remove(destPath)
		return nil, err
	}

	// 6. 创建数据库记录
	asset := model.MediaAsset{
		Filename:     filename,
		OriginalName: file.Filename,
		FilePath:     relativePath,
		FileSize:     file.Size,
		MimeType:     detectMimeType(ext),
		MediaType:    mediaType,
	}

	if err := s.db.Create(&asset).Error; err != nil {
		// 数据库写入失败则删除已保存的文件
		os.Remove(destPath)
		return nil, fmt.Errorf("创建媒体资源记录失败: %w", err)
	}

	return &asset, nil
}

// Delete 删除媒体资源（数据库记录 + 物理文件）。
func (s *MediaService) Delete(id uint64) error {
	var asset model.MediaAsset
	if err := s.db.First(&asset, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAssetNotFound
		}
		return fmt.Errorf("查询媒体资源失败: %w", err)
	}

	// 先删数据库记录
	if err := s.db.Delete(&asset).Error; err != nil {
		return fmt.Errorf("删除媒体资源记录失败: %w", err)
	}

	// 再删物理文件（失败不影响已删除的数据库记录）
	_ = os.Remove(filepath.Join("web", "static", asset.FilePath))

	return nil
}

// ============================================================
// 辅助函数
// ============================================================

// getMediaType 根据扩展名返回媒体类型（精确 map 匹配）。
func getMediaType(ext string) (string, bool) {
	if allowedImageExt[ext] {
		return "image", true
	}
	if allowedVideoExt[ext] {
		return "video", true
	}
	return "", false
}

// validateFileContent 读取文件前 512 字节，使用 net/http 检测真实 MIME 类型，与扩展名对照。
func validateFileContent(path, ext string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取文件进行内容校验失败: %w", err)
	}

	detected := http.DetectContentType(data)
	// 若扩展名为图片类，检测结果应以 "image/" 开头；视频类应以 "video/" 开头
	if strings.HasPrefix(ext, ".") {
		expectedPrefix := ""
		if allowedImageExt[ext] {
			expectedPrefix = "image/"
		} else if allowedVideoExt[ext] {
			expectedPrefix = "video/"
		}
		if expectedPrefix != "" && !strings.HasPrefix(detected, expectedPrefix) {
			return fmt.Errorf("文件内容与扩展名不匹配：扩展名 %s，检测到 %s", ext, detected)
		}
	}
	return nil
}

// detectMimeType 根据扩展名推断 MIME 类型。
func detectMimeType(ext string) string {
	mimeMap := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".bmp":  "image/bmp",
		".svg":  "image/svg+xml",
		".mp4":  "video/mp4",
		".webm": "video/webm",
		".ogv":  "video/ogg",
		".mov":  "video/quicktime",
	}
	if mime, ok := mimeMap[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

// generateUUID 生成 16 字节随机 hex 字符串。
func generateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
