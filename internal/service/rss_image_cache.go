package service

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RSSImageCache 负责把条目正文里的外链图片变成站内可访问的 URL。
//
// 这层接口是为远程预下载准备的：当前只有 LocalImageCache（下载到
// web/static/uploads/rss_img）。将来 homehub-extension 可以在抓取
// feed 的同时预取图片，只要实现本接口（或复用本地实现 + Adopt），
// RSSService 无需改动。
//
// 实现必须：
//   - 尊重 ctx 的取消与超时
//   - 返回 sourceURL → 站内 URL 的映射；失败/跳过的 URL 不出现在 map 中
//   - 对同一 URL 幂等：重复调用不应产生重复文件
type RSSImageCache interface {
	// Cache 缓存一组图片，返回 sourceURL → 站内 URL。
	//
	// entryID 是条目主键：条目按 link 全局唯一、多个源共享同一条内容，
	// 因此图片引用只挂在条目上，与具体源无关。
	Cache(ctx context.Context, entryID uint64, urls []string) map[string]string
	// Sweep 清理没有引用且超过 grace 的缓存文件；grace=0 表示立即清理。
	Sweep(ctx context.Context, grace time.Duration) error
}

// imageURLPrefix 是缓存文件对外的 URL 前缀，必须与 cacheDir 的挂载位置一致。
const imageURLPrefix = "/static/uploads/rss_img/"

// imageResult 是单张图片解析/下载后的结果。
type imageResult struct {
	sourceURL   string
	cacheKey    string
	filename    string
	contentType string
	sizeBytes   int64
	sha256      string
}

// LocalImageCache 把图片下载到本地缓存目录，并把归属关系写入数据库。
//
// 文件按 md5(源 URL) 命名，多个源引用同一 URL 时共享同一份文件；
// RSSImageRef 记录"谁引用过它"，因此删除某个源时不会误删其他源还在
// 使用的图片。
type LocalImageCache struct {
	db           *gorm.DB
	dir          string
	publicPrefix string

	// dbMu 串行化"登记引用"与"清理"两个阶段。
	//
	// 下载本身仍然是并发的；只有写库/删文件需要互斥，避免 Sweep 在
	// 引用行写入之前把刚下载的文件当成孤儿删掉。
	dbMu sync.Mutex

	// pendingMu/pending 记录本进程正在解析的缓存键。
	//
	// Sweep 的宽限期基于 last_seen_at：命中一条 30 天前的老记录时，
	// last_seen_at 不会因为"正在使用"而自动变新，注册又在其后发生。
	// 这个集合覆盖了这段窗口。
	pendingMu sync.Mutex
	pending   map[string]struct{}
}

// markPending 把缓存键标记为进行中，返回取消标记的函数。
func (c *LocalImageCache) markPending(keys []string) func() {
	if len(keys) == 0 {
		return func() {}
	}
	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[string]struct{}, len(keys))
	}
	for _, key := range keys {
		c.pending[key] = struct{}{}
	}
	c.pendingMu.Unlock()

	return func() {
		c.pendingMu.Lock()
		for _, key := range keys {
			delete(c.pending, key)
		}
		c.pendingMu.Unlock()
	}
}

// isPending 判断缓存键是否正在被本进程处理。
func (c *LocalImageCache) isPending(key string) bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	_, ok := c.pending[key]
	return ok
}

// NewLocalImageCache 创建本地图片缓存。
//
// 返回值是具体类型，方便 extension 集成方在注入前调用 Adopt 登记
// 已经预下载到缓存目录的图片；作为 RSSImageCache 使用时无需关心。
func NewLocalImageCache(db *gorm.DB) *LocalImageCache {
	return &LocalImageCache{
		db:           db,
		dir:          cacheDir,
		publicPrefix: imageURLPrefix,
	}
}

// Cache 解析并缓存一组图片，返回 sourceURL → 站内 URL。
func (c *LocalImageCache) Cache(ctx context.Context, entryID uint64, urls []string) map[string]string {
	out := make(map[string]string)
	if len(urls) == 0 {
		return out
	}

	maxBytes, concurrency, allowPrivate := imageCacheLimits()

	// 去重并按配置并发解析。失败只记录 debug：一张图 404 不应刷屏。
	seen := make(map[string]struct{}, len(urls))
	pending := make([]string, 0, len(urls))
	for _, src := range urls {
		if src == "" {
			continue
		}
		if _, ok := seen[src]; ok {
			continue
		}
		seen[src] = struct{}{}
		pending = append(pending, src)
	}

	// 解析开始前就标记，覆盖"命中老记录 → register 写引用"的整段窗口。
	release := c.markPending(keysOf(pending))
	defer release()

	var mu sync.Mutex
	results := make([]*imageResult, 0, len(pending))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, src := range pending {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			res, err := c.resolve(ctx, src, maxBytes, allowPrivate)
			if err != nil {
				logger.Debug("[rss] 图片缓存跳过", "url", src, "error", err)
				return
			}

			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}(src)
	}
	wg.Wait()

	if len(results) == 0 {
		return out
	}

	if ctx.Err() != nil {
		return out
	}
	c.register(entryID, results)

	for _, res := range results {
		out[res.sourceURL] = c.publicPrefix + res.filename
	}
	return out
}

// resolve 按"数据库记录 → 已有文件 → 下载"的顺序解析一张图片。
func (c *LocalImageCache) resolve(ctx context.Context, src string, maxBytes int64, allowPrivate bool) (*imageResult, error) {
	key := md5Hex(src)

	// 1. 数据库已有记录且文件仍在：直接复用（历史文件也能在此被认领）。
	if c.db != nil {
		var img model.RSSImage
		err := c.db.Where("cache_key = ?", key).First(&img).Error
		if err == nil {
			if info, statErr := os.Stat(filepath.Join(c.dir, img.Filename)); statErr == nil && info.Size() > 0 {
				return &imageResult{
					sourceURL:   src,
					cacheKey:    key,
					filename:    img.Filename,
					contentType: img.ContentType,
					sizeBytes:   info.Size(),
					sha256:      img.SHA256,
				}, nil
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("查询图片记录失败: %w", err)
		}
	}

	// 2. 升级前的缓存没有数据库记录：按文件名找到就直接认领，避免重下。
	if name, info := c.findCachedFile(key); name != "" {
		return &imageResult{
			sourceURL:   src,
			cacheKey:    key,
			filename:    name,
			contentType: contentTypeForExt(filepath.Ext(name)),
			sizeBytes:   info.Size(),
		}, nil
	}

	// 3. 真正下载。
	return c.download(ctx, src, key, maxBytes, allowPrivate)
}

// findCachedFile 在缓存目录里查找 `rss_<key>.*` 形式的已有文件。
func (c *LocalImageCache) findCachedFile(key string) (string, os.FileInfo) {
	matches, err := filepath.Glob(filepath.Join(c.dir, "rss_"+key+".*"))
	if err != nil {
		return "", nil
	}
	for _, path := range matches {
		name := filepath.Base(path)
		if strings.HasSuffix(name, ".part") {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return name, info
		}
	}
	return "", nil
}

// download 下载单张图片，校验类型/大小后原子落盘。
//
// 关键点：先用 maxBytes+1 读取，超过上限立即失败并删除临时文件。
// 旧实现用 io.LimitReader(body, 2MB) 直接写目标文件，超过 2MB 的图片
// 会被截断却仍被当作成功，磁盘上留下无法解码的坏文件。
func (c *LocalImageCache) download(ctx context.Context, src, key string, maxBytes int64, allowPrivate bool) (*imageResult, error) {
	if err := validateRemoteURL(ctx, src, allowPrivate); err != nil {
		return nil, err
	}

	// 图片 URL 来自第三方 RSS 内容，默认走连接层校验的严格客户端；
	// 显式放行内网时才换用不校验的客户端。
	client := rssImageHTTPClient()
	if allowPrivate {
		client = rssLooseHTTPClient()
	}
	resp, err := client.R().SetContext(ctx).Get(src)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP 状态码 %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	// Peek 不消耗 body；响应体不足 512 字节时 peek 会带上 io.EOF，但数据仍可用。
	peek, _ := reader.Peek(512)

	contentType := normalizeContentType(resp.Header.Get("Content-Type"))
	sniffed := normalizeContentType(http.DetectContentType(peek))
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = sniffed
	}
	// 源站可能把 HTML 错误页标成 image/jpeg；明确的 HTML 一律拒绝。
	if strings.HasPrefix(sniffed, "text/html") {
		return nil, fmt.Errorf("响应是 HTML 而不是图片: %s", contentType)
	}
	if !isAllowedImageContentType(contentType, peek, src) {
		return nil, fmt.Errorf("响应不是图片: %s", contentType)
	}

	ext := imageExt(contentType, src)
	filename := "rss_" + key + ext
	finalPath := filepath.Join(c.dir, filename)

	// 并发下载同一 URL 时，先到者已经落盘，后来者直接复用。
	if info, statErr := os.Stat(finalPath); statErr == nil && info.Size() > 0 {
		return &imageResult{
			sourceURL:   src,
			cacheKey:    key,
			filename:    filename,
			contentType: contentType,
			sizeBytes:   info.Size(),
		}, nil
	}

	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return nil, fmt.Errorf("创建缓存目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(c.dir, ".rss-*.part")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	// 失败路径统一清理，成功 rename 后 Remove 是空操作。
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("写入图片失败: %w", err)
	}
	if n > maxBytes {
		return nil, fmt.Errorf("图片超过上限 %d 字节", maxBytes)
	}
	if n == 0 {
		return nil, errors.New("图片内容为空")
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("关闭临时文件失败: %w", err)
	}

	// 先写临时文件再 rename，避免并发读到写了一半的图片。
	if err := os.Rename(tmpPath, finalPath); err != nil {
		// rename 失败可能是目标已被并发写入；只要目标有效就复用。
		if info, statErr := os.Stat(finalPath); statErr == nil && info.Size() > 0 {
			return &imageResult{
				sourceURL:   src,
				cacheKey:    key,
				filename:    filename,
				contentType: contentType,
				sizeBytes:   info.Size(),
			}, nil
		}
		return nil, fmt.Errorf("保存图片失败: %w", err)
	}

	return &imageResult{
		sourceURL:   src,
		cacheKey:    key,
		filename:    filename,
		contentType: contentType,
		sizeBytes:   n,
		sha256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

// AdoptedImage 描述一张由外部（如 homehub-extension）预下载、
// 已经落在本地缓存目录里的图片。
type AdoptedImage struct {
	SourceURL   string // 原始图片 URL
	Filename    string // 缓存目录内的文件名（不允许包含路径分隔符）
	ContentType string // 可留空，按扩展名推断
	SizeBytes   int64  // 可留空，按实际文件大小补全
	SHA256      string // 可留空
}

// Adopt 登记外部预下载的图片，并建立条目引用。
//
// 这是给远程预下载实现准备的：extension 负责把图片落到 HomeHub 的缓存
// 目录（原子写入），然后调用 Adopt 登记归属；后续 resolve 会直接命中
// 文件，不会重复下载。返回 sourceURL → 站内 URL。
//
// 不存在的文件、包含路径分隔符的文件名会被跳过，避免把任意路径写进
// 资源表。
func (c *LocalImageCache) Adopt(entryID uint64, items []AdoptedImage) map[string]string {
	out := make(map[string]string)
	if len(items) == 0 {
		return out
	}

	results := make([]*imageResult, 0, len(items))
	for _, item := range items {
		if item.SourceURL == "" || item.Filename == "" {
			continue
		}
		if filepath.Base(item.Filename) != item.Filename {
			continue
		}
		info, err := os.Stat(filepath.Join(c.dir, item.Filename))
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			continue
		}

		contentType := item.ContentType
		if contentType == "" {
			contentType = contentTypeForExt(filepath.Ext(item.Filename))
		}
		size := item.SizeBytes
		if size <= 0 {
			size = info.Size()
		}

		results = append(results, &imageResult{
			sourceURL:   item.SourceURL,
			cacheKey:    md5Hex(item.SourceURL),
			filename:    item.Filename,
			contentType: contentType,
			sizeBytes:   size,
			sha256:      item.SHA256,
		})
		out[item.SourceURL] = c.publicPrefix + item.Filename
	}

	if len(results) > 0 {
		c.register(entryID, results)
	}
	return out
}

// register 把解析结果写入资源表，并登记 feed/条目引用。
//
// 数据库不可用时只丢失归属信息，不影响本次抓取的图片展示；下次抓取
// 会重新登记。
func (c *LocalImageCache) register(entryID uint64, results []*imageResult) {
	if c.db == nil || len(results) == 0 {
		return
	}

	now := time.Now()
	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	rows := make([]model.RSSImage, 0, len(results))
	keys := make([]string, 0, len(results))
	for _, res := range results {
		rows = append(rows, model.RSSImage{
			CacheKey:    res.cacheKey,
			SourceURL:   res.sourceURL,
			Filename:    res.filename,
			ContentType: res.contentType,
			SizeBytes:   res.sizeBytes,
			SHA256:      res.sha256,
			LastSeenAt:  now,
		})
		keys = append(keys, res.cacheKey)
	}

	// 同一个 URL 可能被并发抓取：冲突时更新为本次解析结果。
	if err := c.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"source_url", "filename", "content_type", "size_bytes", "sha256", "last_seen_at", "updated_at",
		}),
	}).Create(&rows).Error; err != nil {
		logger.Warn("[rss] 登记图片资源失败", "error", err)
		return
	}

	var stored []model.RSSImage
	if err := c.db.Where("cache_key IN ?", keys).Find(&stored).Error; err != nil {
		logger.Warn("[rss] 查询图片资源失败", "error", err)
		return
	}
	idByKey := make(map[string]uint64, len(stored))
	for _, img := range stored {
		idByKey[img.CacheKey] = img.ID
	}

	// 没有条目 ID 时无法建立引用（条目还没入库），只登记资源本身。
	if entryID == 0 {
		return
	}

	refs := make([]model.RSSImageRef, 0, len(stored))
	for _, key := range keys {
		id, ok := idByKey[key]
		if !ok {
			continue
		}
		refs = append(refs, model.RSSImageRef{
			ImageID: id,
			EntryID: entryID,
		})
	}
	if len(refs) == 0 {
		return
	}
	if err := c.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&refs).Error; err != nil {
		logger.Warn("[rss] 登记图片引用失败", "error", err)
	}
}

// Sweep 清理没有引用且超过 grace 的图片，以及缓存目录中没有数据库记录
// 的历史文件（升级前的遗留）。grace=0 用于删除订阅源/条目后的立即回收。
func (c *LocalImageCache) Sweep(ctx context.Context, grace time.Duration) error {
	if c.db == nil {
		return nil
	}
	if cfg := config.Get(); cfg != nil && !cfg.RSS.ImageCleanupEnabled {
		return nil
	}
	immediate := grace <= 0
	if immediate {
		// grace=0 也保留 1 分钟缓冲：删除源/条目后的立即回收可能与
		// 并发抓取的"下载完成、引用尚未写入"窗口重叠。
		grace = time.Minute
	}
	cutoff := time.Now().Add(-grace)

	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	// 1. 对账引用：删除没有对应条目的引用。
	//
	// 引用只挂在条目上（不区分源），所以"找不到条目"是唯一的脏数据
	// 形态：条目被删除时引用删除中断，或历史数据遗留。脏引用会让图片
	// 永远"有引用"、无法回收。
	//
	// 只在定期清理时做：抓取流程是"处理完所有条目再批量入库"，
	// 中间可能持续几十秒。删除源/retention 触发的立即回收若在此刻
	// 做全量对账，会把正在入库的条目的合法引用当成脏数据删掉。
	if !immediate {
		if err := c.reconcileRefs(ctx); err != nil {
			logger.Warn("[rss] 图片引用对账失败", "error", err)
		}
	}

	// 2. 数据库里"无人引用且已过保留期"的图片。
	var orphaned []model.RSSImage
	if err := c.db.
		Where("last_seen_at < ?", cutoff).
		Where("NOT EXISTS (SELECT 1 FROM homehub_rss_image_refs WHERE homehub_rss_image_refs.image_id = homehub_rss_images.id)").
		Find(&orphaned).Error; err != nil {
		return fmt.Errorf("查询孤儿图片失败: %w", err)
	}

	removed := 0
	for _, img := range orphaned {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.isPending(img.CacheKey) {
			continue
		}
		path := filepath.Join(c.dir, img.Filename)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logger.Warn("[rss] 删除图片文件失败", "file", img.Filename, "error", err)
			continue
		}
		// 物理删除：软删除会让 cache_key 唯一索引永远无法复用。
		if err := c.db.Unscoped().Delete(&model.RSSImage{}, img.ID).Error; err != nil {
			logger.Warn("[rss] 删除图片记录失败", "id", img.ID, "error", err)
			continue
		}
		removed++
	}

	// 3. 目录中没有任何数据库记录的历史文件。
	//
	// 只在定期清理（grace>0）时执行：删除源后的立即回收可能和并发抓取
	// 的"下载完成、尚未登记"窗口重叠，此时按 mtime 判断会误删新文件。
	if immediate {
		if removed > 0 {
			logger.Info("[rss] 图片缓存清理完成", "removed", removed, "grace", grace.String())
		}
		return nil
	}
	var known []string
	if err := c.db.Model(&model.RSSImage{}).Pluck("filename", &known).Error; err != nil {
		return fmt.Errorf("查询已登记图片失败: %w", err)
	}
	knownSet := make(map[string]struct{}, len(known))
	for _, name := range known {
		knownSet[name] = struct{}{}
	}

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取图片缓存目录失败: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, ok := knownSet[name]; ok {
			continue
		}
		// 只处理本模块的命名，避免误删目录里的其他文件。
		isPart := strings.HasPrefix(name, ".rss-") && strings.HasSuffix(name, ".part")
		if !isPart && !strings.HasPrefix(name, "rss_") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, name)); err != nil && !os.IsNotExist(err) {
			logger.Warn("[rss] 删除历史缓存失败", "file", name, "error", err)
			continue
		}
		removed++
	}

	if removed > 0 {
		logger.Info("[rss] 图片缓存清理完成", "removed", removed, "grace", grace.String())
	}
	return nil
}

// legacyFileNameRe 匹配本模块生成的缓存文件名。
var legacyFileNameRe = regexp.MustCompile(`^rss_([0-9a-f]{32})\.([A-Za-z0-9]{2,5})$`)

// legacyPathRe 从条目正文中提取站内图片文件名。
var legacyPathRe = regexp.MustCompile(`/static/uploads/rss_img/(rss_[0-9a-f]{32}\.[A-Za-z0-9]{2,5})`)

// BackfillLegacy 为升级前已经缓存的图片补登资源记录与引用。
//
// 为什么必须有：升级前的图片只有文件、没有数据库记录。若直接开始清理，
// 这些文件会因为"无人引用"被当成孤儿删除，历史条目里的图片随即 404。
// 回填后它们和正常缓存的图片走同一套引用计数。
//
// 幂等且可重复执行：cache_key 由文件名反解（与当初的 md5(URL) 一致），
// 因此同一个 URL 再次被抓取时也能直接命中这条历史记录。source_url 留空
// 表示"来源未知"，不影响引用与清理判断。
func (c *LocalImageCache) BackfillLegacy(ctx context.Context) error {
	if c.db == nil {
		return nil
	}

	var entries []model.RSSEntry
	result := c.db.
		Select("id", "content").
		Where("content LIKE ?", "%/static/uploads/rss_img/%").
		FindInBatches(&entries, 500, func(tx *gorm.DB, batch int) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			c.backfillBatch(entries)
			entries = entries[:0]
			return nil
		})
	if result.Error != nil {
		return fmt.Errorf("回填历史图片引用失败: %w", result.Error)
	}
	return nil
}

// backfillBatch 处理一批条目。
func (c *LocalImageCache) backfillBatch(entries []model.RSSEntry) {
	// 1. 收集本批涉及的文件名（同一文件可能被多个条目引用）。
	type pendingRef struct {
		filename string
		entryID  uint64
	}
	var refs []pendingRef
	fileSet := make(map[string]struct{})

	for _, entry := range entries {
		if entry.ID == 0 {
			continue
		}
		seen := make(map[string]struct{})
		for _, m := range legacyPathRe.FindAllStringSubmatch(entry.Content, -1) {
			if len(m) < 2 {
				continue
			}
			name := m[1]
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			fileSet[name] = struct{}{}
			refs = append(refs, pendingRef{filename: name, entryID: entry.ID})
		}
	}
	if len(fileSet) == 0 {
		return
	}

	// 2. 只为磁盘上真实存在的文件建档，避免把已丢失的图片重新登记进来。
	now := time.Now()
	images := make([]model.RSSImage, 0, len(fileSet))
	for name := range fileSet {
		m := legacyFileNameRe.FindStringSubmatch(name)
		if len(m) < 3 {
			continue
		}
		info, err := os.Stat(filepath.Join(c.dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			continue
		}
		images = append(images, model.RSSImage{
			CacheKey:    m[1],
			SourceURL:   "",
			Filename:    name,
			ContentType: contentTypeForExt(filepath.Ext(name)),
			SizeBytes:   info.Size(),
			LastSeenAt:  now,
		})
	}
	if len(images) == 0 {
		return
	}

	if err := c.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		DoNothing: true,
	}).Create(&images).Error; err != nil {
		logger.Warn("[rss] 回填图片记录失败", "error", err)
		return
	}

	// 3. 查回 ID，建立引用。
	var stored []model.RSSImage
	if err := c.db.Where("filename IN ?", filenamesOf(images)).Find(&stored).Error; err != nil {
		logger.Warn("[rss] 查询回填图片失败", "error", err)
		return
	}
	idByFile := make(map[string]uint64, len(stored))
	for _, img := range stored {
		idByFile[img.Filename] = img.ID
	}

	rows := make([]model.RSSImageRef, 0, len(refs))
	for _, ref := range refs {
		id, ok := idByFile[ref.filename]
		if !ok || ref.entryID == 0 {
			continue
		}
		rows = append(rows, model.RSSImageRef{
			ImageID: id,
			EntryID: ref.entryID,
		})
	}
	if len(rows) == 0 {
		return
	}
	if err := c.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
		logger.Warn("[rss] 回填图片引用失败", "error", err)
		return
	}
}

// keysOf 计算一批源 URL 的缓存键。
func keysOf(urls []string) []string {
	keys := make([]string, 0, len(urls))
	for _, u := range urls {
		keys = append(keys, md5Hex(u))
	}
	return keys
}

// filenamesOf 提取模型的文件名列表。
func filenamesOf(images []model.RSSImage) []string {
	names := make([]string, 0, len(images))
	for _, img := range images {
		names = append(names, img.Filename)
	}
	return names
}

// refReconcileMinAge 是对账只处理的引用最小年龄。
//
// 第二道保险：即使将来有人在抓取路径上调用对账，刚写入的引用也不会
// 被误删（条目批量插入在图片处理之后，正常情况下不会超过抓取预算）。
const refReconcileMinAge = 30 * time.Minute

// reconcileRefs 删除没有对应条目的图片引用。
//
// 引用在条目入库之后写入，正常不会悬空；但保留策略或孤儿回收如果中途
// 失败，就可能留下指向已删条目的引用。脏引用会让图片永远"有引用"、
// 无法回收，所以定期用一条 NOT EXISTS 清掉。
//
// 只处理创建超过 refReconcileMinAge 的引用，避免与"条目刚插入、引用刚
// 写入"的窗口重叠。
func (c *LocalImageCache) reconcileRefs(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	result := c.db.Unscoped().
		Where("created_at < ?", time.Now().Add(-refReconcileMinAge)).
		Where("NOT EXISTS (SELECT 1 FROM homehub_rss_entries e WHERE e.id = homehub_rss_image_refs.entry_id)").
		Delete(&model.RSSImageRef{})
	if result.Error != nil {
		return fmt.Errorf("清理无效图片引用失败: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		logger.Info("[rss] 清理无主图片引用", "removed", result.RowsAffected)
	}
	return nil
}

// imageCacheLimits 读取图片缓存的运行参数（配置热更新即时生效）。
func imageCacheLimits() (maxBytes int64, concurrency int, allowPrivate bool) {
	maxBytes = 10 * 1024 * 1024
	concurrency = 6
	cfg := config.Get()
	if cfg == nil {
		return maxBytes, concurrency, false
	}
	if cfg.RSS.ImageMaxBytes > 0 {
		maxBytes = cfg.RSS.ImageMaxBytes
	}
	if cfg.RSS.ImageCacheConcurrency > 0 {
		concurrency = cfg.RSS.ImageCacheConcurrency
	}
	// 并发数 <=0 时 make(chan struct{}, 0) 是无缓冲 channel，
	// 第一次发送就会永久阻塞。配置校验会拦住非法值，这里再兜一层。
	if concurrency <= 0 {
		concurrency = 6
	}
	return maxBytes, concurrency, cfg.RSS.ImageAllowPrivateHosts
}

// imageAllowPrivate 返回是否允许图片下载访问内网/回环地址（热更新生效）。
func imageAllowPrivate() bool {
	cfg := config.Get()
	return cfg != nil && cfg.RSS.ImageAllowPrivateHosts
}

// imageCleanupInterval 返回后台清理间隔。
func imageCleanupInterval() time.Duration {
	hours := 24
	if cfg := config.Get(); cfg != nil && cfg.RSS.ImageCleanupIntervalHours > 0 {
		hours = cfg.RSS.ImageCleanupIntervalHours
	}
	return time.Duration(hours) * time.Hour
}

// imageRetentionGrace 返回无引用图片的保留期。
func imageRetentionGrace() time.Duration {
	days := 30
	if cfg := config.Get(); cfg != nil && cfg.RSS.ImageRetentionDays > 0 {
		days = cfg.RSS.ImageRetentionDays
	}
	return time.Duration(days) * 24 * time.Hour
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func normalizeContentType(ct string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
}

// isAllowedImageContentType 判断响应是否真的是图片。
//
// 不只看声明的 Content-Type：源站可能把 HTML 错误页标成 image/jpeg。
// 这里同时要求嗅探结果或扩展名/内容特征能对上。SVG 是 XML，标准库会
// 嗅探成 text/xml，因此单独按 `.svg` + 内容含 `<svg` 放行。
func isAllowedImageContentType(contentType string, peek []byte, src string) bool {
	ct := normalizeContentType(contentType)
	if strings.HasPrefix(ct, "image/") {
		return true
	}
	if strings.EqualFold(filepath.Ext(strings.SplitN(src, "?", 2)[0]), ".svg") {
		return strings.Contains(strings.ToLower(string(peek)), "<svg")
	}
	return false
}

// imageExt 根据 Content-Type 选择扩展名，回落到源 URL 的扩展名。
func imageExt(contentType, src string) string {
	switch normalizeContentType(contentType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/bmp":
		return ".bmp"
	case "image/avif":
		return ".avif"
	case "image/tiff":
		return ".tiff"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	}

	clean := strings.SplitN(src, "?", 2)[0]
	if ext := strings.ToLower(filepath.Ext(clean)); isImageExt(ext) {
		return ext
	}
	return ".png"
}

// contentTypeForExt 用于给历史缓存文件补一个 Content-Type。
func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".ico":
		return "image/x-icon"
	}
	return "application/octet-stream"
}

// validateRemoteURL 校验即将访问的远程地址，防止把 HomeHub 当成 SSRF 跳板。
//
// 除了字面 IP 与主机名黑名单，还会解析域名并校验全部 A/AAAA 记录：
// 只查字面 IP 会被 DNS rebinding 绕过。allowPrivate 为 true 时跳过内网
// 限制（图片可由 rss.image_allow_private_hosts 放行，feed 不允许）。
func validateRemoteURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("无效的图片 URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("不支持的图片协议: %s", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("图片 URL 缺少主机名")
	}
	if allowPrivate {
		return nil
	}
	if err := validateFeedURL(rawURL); err != nil {
		return err
	}
	// 域名还要解析一次，防止 DNS 指向内网（validateFeedURL 只看了字面 IP）。
	if net.ParseIP(host) != nil {
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("解析图片主机失败: %w", err)
	}
	for _, addr := range ips {
		if isBlockedIP(addr.IP) {
			return fmt.Errorf("图片主机解析到内部地址: %s", addr.IP)
		}
	}
	return nil
}

// blockedCIDRs 是 Inet.IsPrivate 未覆盖、但同样不应访问的保留网段。
//
// 0.0.0.0 在 Linux 上会连到本机；100.64.0.0/10 是运营商级 NAT；
// 198.18.0.0/15 是基准测试网段。这些地址出现在公网 RSS 里没有正当理由。
var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"100.64.0.0/10",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4",
		"2001:db8::/32",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// isBlockedIP 判断一个 IP 是否属于 SSRF 敏感范围。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return true
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
