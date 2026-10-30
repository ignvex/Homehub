package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
)

// 上传相关常量。
const (
	maxUploadSize = 50 << 20 // 50 MB
)

var (
	// ErrFileTooLarge 文件超过大小限制。
	ErrFileTooLarge = errors.New("文件大小不能超过 50MB")
	// ErrInvalidFileType 不支持的文件类型。
	ErrInvalidFileType = errors.New("不支持的文件类型，请上传常见图片、视频、音频或 PDF")
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

// BackgroundKinds 是可以用作页面背景的媒体类型。
//
// 音频与 PDF 可以上传与展示，但不能当背景：浏览器无法把音频渲染成
// 背景层，PDF 只能内联打开。前端拿到非这两类的资源会渲染错位，
// 因此在数据出口就过滤掉，而不是依赖前端判断。
var BackgroundKinds = []string{string(CategoryImage), string(CategoryVideo)}

// FilterBackgroundIDs 返回 ids 中确实存在、且类型可作背景的那些，保持原顺序。
//
// 用于写入背景配置前清洗：即便前端传了音频 ID，也不会被持久化。
func (s *MediaService) FilterBackgroundIDs(ids []uint64) ([]uint64, error) {
	// 必须用 ListBackgroundAssets 而不是 ListByIDs：后者不筛类型，
	// 会让音频/PDF 的 ID 混进背景配置。
	assets, err := s.ListBackgroundAssets(ids)
	if err != nil {
		return nil, err
	}

	allowed := make(map[uint64]bool, len(assets))
	for _, a := range assets {
		allowed[a.ID] = true
	}

	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// ListBackgroundAssets 按 ID 顺序返回可作背景的媒体资源。
//
// 与 ListByIDs 的区别是额外过滤类型；用于 /webui 这类对外输出。
func (s *MediaService) ListBackgroundAssets(ids []uint64) ([]model.MediaAsset, error) {
	assets, err := s.ListByIDs(ids)
	if err != nil {
		return nil, err
	}

	out := make([]model.MediaAsset, 0, len(assets))
	for _, a := range assets {
		if a.MediaType == string(CategoryImage) || a.MediaType == string(CategoryVideo) {
			out = append(out, a)
		}
	}
	return out, nil
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
func (s *MediaService) Upload(owner *model.User, file *multipart.FileHeader) (*model.MediaAsset, error) {
	// 1. 校验文件大小
	if file.Size > maxUploadSize {
		return nil, ErrFileTooLarge
	}

	// 2. 校验扩展名（拒绝空扩展名，防止绕过检查）
	ext := normalizeExt(file.Filename)
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
	// 图片补充像素尺寸（仅标准库可解码的格式），失败留 0 不影响上传。
	var width, height int
	if mediaType == string(CategoryImage) {
		width, height = imageDimensions(destPath)
	}

	ownerID := uint64(0)
	if owner != nil {
		ownerID = owner.ID
	}

	asset := model.MediaAsset{
		Filename: filename,
		// OriginalName 直接来自客户端 multipart 的文件名，长度不受控，
		// 而列是 varchar(255)：不裁剪会让上传在最后一步失败（文件已写盘、
		// 记录插入被 Postgres 拒收）。按字符截断，避免切断多字节字符。
		OriginalName: model.TruncateRunes(file.Filename, 255),
		FilePath:     relativePath,
		FileSize:     file.Size,
		// 内容哈希：图床场景下用于完整性校验，也为将来“同一张图只存一份”
		// 的去重留出依据。这一步读已经落盘的文件，不额外消耗一次上传流。
		SHA256:    fileSHA256(destPath),
		MimeType:  detectMimeType(ext),
		MediaType: mediaType,
		Source:    MediaSourceUpload,
		OwnerID:   ownerID,
		Width:     width,
		Height:    height,
	}

	if err := s.db.Create(&asset).Error; err != nil {
		// 数据库写入失败则删除已保存的文件
		os.Remove(destPath)
		return nil, fmt.Errorf("创建媒体资源记录失败: %w", err)
	}

	return &asset, nil
}

// Delete 删除媒体资源（数据库记录 + 物理文件）。
//
// operator 用于归属校验：只有上传者本人，或 1–3 级（editor 及以上）能删。
// 少了这条校验，任何登录用户都能删掉别人正在当背景用的图 —— 这是本仓库
// 改造前真实存在的水平越权。
func (s *MediaService) Delete(id uint64, operator *model.User) error {
	if operator == nil {
		return ErrForbidden
	}
	var asset model.MediaAsset
	if err := s.db.First(&asset, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAssetNotFound
		}
		return fmt.Errorf("查询媒体资源失败: %w", err)
	}

	// 归属校验：本人，或 1–3 级可以删。
	//
	// 用 !CanDeleteOwned 而不是简单的 OwnerID 比较，是因为 editor(3)
	// 按设计能管全部内容；member(4) 只能管自己的。
	if !canManageOwned(operator, asset.OwnerID) {
		return ErrForbidden
	}

	// 物理删除：这条记录代表一份已落盘的图床文件，文件删了之后软删除
	// 提供不了任何恢复能力，只会让记录与磁盘长期不一致。
	//
	// 注意顺序：先删记录再删文件。反过来若删文件成功、删记录失败，
	// 库里会留下指向不存在文件的记录，列表里就是一片碎图。
	if err := s.db.Unscoped().Delete(&asset).Error; err != nil {
		return fmt.Errorf("删除媒体资源记录失败: %w", err)
	}

	// 再删物理文件（失败不影响已删除的数据库记录）
	_ = os.Remove(filepath.Join("web", "static", asset.FilePath))

	return nil
}

// ============================================================
// 辅助函数
// ============================================================

// generateUUID 生成 16 字节随机 hex 字符串。
func generateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// canManageOwned 判断 operator 能否管理一条带 owner 的内容。
//
// 规则：
//   - 1–3 级（admin/operator/editor）：可以管全部；
//   - 4 级（member）：只能管自己的（ownerID 相同）；
//   - 5 级（viewer）：只读，什么都不行；
//   - ownerID 为 0 的历史数据：视为无归属，只有 1–3 级能动。
func canManageOwned(operator *model.User, ownerID uint64) bool {
	if operator == nil {
		return false
	}
	if operator.HasLevel(model.LevelEditor) {
		return true
	}
	if !operator.HasLevel(model.LevelMember) {
		return false
	}
	return ownerID != 0 && operator.ID == ownerID
}
