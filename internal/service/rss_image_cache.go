package service

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/rand"
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
	"strconv"
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
	//
	// 返回的 error 只表示**登记失败**（数据库不可用等），不表示“某张图下
	// 载失败”——后者是常态，只体现在 map 里缺少相应 key。
	//
	// 调用方必须区分这两者：登记失败时不要把清洗结果写进库。正文一旦
	// 落库就不会重算（fillEntryContent 只处理空正文），写进去就等于把
	// 这批图片永久丢掉了。
	Cache(ctx context.Context, entryID uint64, urls []string) (map[string]string, error)
	// Sweep 清理没有引用且超过 grace 的缓存文件；grace=0 表示立即清理。
	Sweep(ctx context.Context, grace time.Duration) error
}

// imageURLPrefix 是缓存文件对外的 URL 前缀，必须与 cacheDir 的挂载位置一致。
const imageURLPrefix = "/static/uploads/rss_img/"

// imageResult 是单张图片解析/下载后的结果。
type imageResult struct {
	sourceURL   string
	cacheKey    string // 内容 SHA256（旧数据可能是 md5(URL)）
	filename    string
	urlKey      string // sha256(源 URL)，用于写 RSSImageSource
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

	// pendingMu/pending 记录本进程正在解析的缓存键及其引用计数。
	//
	// Sweep 的宽限期基于 last_seen_at：命中一条 30 天前的老记录时，
	// last_seen_at 不会因为"正在使用"而自动变新，注册又在其后发生。
	// 这个集合覆盖了这段窗口。
	//
	// 用计数而不是集合：两个并发抓取批次可能解析到**同一个内容键**
	// （不同源转载了同一篇带图文章，或时效签名 URL 指向同一张图）。
	// 若用集合，先完成的批次 release 时 delete 掉该键，后完成的那批
	// 就失去了保护——此时它的文件尚未登记引用，Sweep 会把文件删掉，
	// 随后 register 拿着已删除的文件名写库，形成死链。
	pendingMu sync.Mutex
	pending   map[string]int

	// memURL 是进程内的 URL 键 → 已解析结果 的映射。
	//
	// 两个作用：数据库不可用时它是唯一的复用手段（否则同一 URL
	// 每轮都会被重新下载）；数据库可用时它省一次查询。
	//
	// 存的不是一个文件名而是一个完整结果：命中后仍需返回 cacheKey，
	// 否则 register 会以“没有内容键”为由跳过登记，图片引用就不会
	// 重建——新条目会成为无图引用，而旧引用的 last_seen_at 也永远
	// 不再更新。
	//
	// 命中后仍要 stat 文件：Sweep 可能已经把文件删了，只信内存会
	// 返回死链。
	//
	// 必须有容量上限：时效签名 URL 每轮抓取都会产生新的 urlKey，
	// 不设上限的 map 会随时间单调增长——HomeHub 是长期跑在 NAS 上的
	// 常驻进程，这种泄漏不会自己恢复。
	memMu  sync.RWMutex
	memURL map[string]memEntry

	// memContent 是内容哈希 → 已解析结果 的反向映射。
	//
	// 用途：并发下载同一内容（不同 URL、相同字节）时，第二个能直接
	// 命中第一个刚落盘的文件，不必再写一份。数据库路径靠
	// lookupByContent 做同一件事，但那条记录要等整批 register 之后
	// 才存在，所以批次内并发时它看不到；无数据库时更是只有这里能用。
	memContent map[string]memEntry

	// contentMu 串行化“算完内容哈希 → 决定落盘还是复用”。
	//
	// 不加锁时多个 goroutine 会同时 miss 两个查重入口，各自落一份文件。
	// 锁内只有 rename 与内存记录（数据已写入临时文件），耗时很短。
	contentMu sync.Mutex
}

// memURLLimit 是进程内 URL 映射的容量上限。
//
// 超限时整体清空而不是逐条淘汰：这是纯缓存（清空只会导致下一轮多几次
// 查询或下载，不影响正确性），但逐条淘汰需要 LRU 结构，代价与收益不成
// 正比。选 4096：对应几千个不同图片 URL，占用几十 KB 级别，且远超一次
// 抓取轮次涉及的图片数（单个源通常几十张）。
const memURLLimit = 4096

// memEntry 是进程内缓存的单条解析结果。
type memEntry struct {
	filename    string
	cacheKey    string
	contentType string
}

// memLookup 读取进程内映射。
func (c *LocalImageCache) memLookup(urlKey string) (memEntry, bool) {
	if urlKey == "" {
		return memEntry{}, false
	}
	c.memMu.RLock()
	defer c.memMu.RUnlock()
	e, ok := c.memURL[urlKey]
	return e, ok
}

// memRemember 记录进程内映射。
func (c *LocalImageCache) memRemember(urlKey string, e memEntry) {
	if urlKey == "" || e.filename == "" {
		return
	}
	c.memMu.Lock()
	defer c.memMu.Unlock()

	if c.memURL == nil {
		c.memURL = make(map[string]memEntry)
	}
	if len(c.memURL) >= memURLLimit {
		// 达上限就整体重置。保留正确性（命中的文件仍会被 stat 校验），
		// 代价只是下一轮多查几次库。
		c.memURL = make(map[string]memEntry, memURLLimit)
	}
	c.memURL[urlKey] = e
}

// memForget 丢弃失效的进程内映射。
func (c *LocalImageCache) memForget(urlKey string) {
	c.memMu.Lock()
	delete(c.memURL, urlKey)
	c.memMu.Unlock()
}

// memLookupContent 按内容哈希读取进程内映射。
func (c *LocalImageCache) memLookupContent(contentHash string) (memEntry, bool) {
	if contentHash == "" {
		return memEntry{}, false
	}
	c.memMu.RLock()
	defer c.memMu.RUnlock()
	e, ok := c.memContent[contentHash]
	return e, ok
}

// memRememberContent 按内容哈希记录进程内映射（有容量上限）。
func (c *LocalImageCache) memRememberContent(contentHash string, e memEntry) {
	if contentHash == "" || e.filename == "" {
		return
	}
	c.memMu.Lock()
	defer c.memMu.Unlock()

	if c.memContent == nil {
		c.memContent = make(map[string]memEntry)
	}
	if len(c.memContent) >= memURLLimit {
		c.memContent = make(map[string]memEntry, memURLLimit)
	}
	c.memContent[contentHash] = e
}

// memForgetContent 丢弃失效的内容映射。
func (c *LocalImageCache) memForgetContent(contentHash string) {
	c.memMu.Lock()
	delete(c.memContent, contentHash)
	c.memMu.Unlock()
}

// pendingTracker 跟踪本进程正在处理的缓存键，并在结束时释放。
//
// 需要可变是因为键的形态在流程中会变：下载前只知道 URL 键，
// 下载完成后才知道内容哈希。两者都要覆盖，否则 Sweep 会在
// “记录已存在但 last_seen_at 尚未更新”的窗口里把文件删掉。
type pendingTracker struct {
	cache *LocalImageCache
	mu    sync.Mutex
	keys  map[string]struct{}
}

// add 把一批键加入跟踪集合。
//
// 必须同时持两把锁：t.mu 保护自己的 keys 集合，
// t.cache.pendingMu 保护共享的 pending map（Sweep 会并发读它）。
// 只加前者会造成 map 并发写 panic。
//
// 引用计数只在首次加入时递增：t.keys 是集合，重复 add 同一个键
// 不会让计数虚高（否则 release 一次减不回去，键永远挂着）。
func (t *pendingTracker) add(keys ...string) {
	if t == nil || len(keys) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cache.pendingMu.Lock()
	defer t.cache.pendingMu.Unlock()

	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, dup := t.keys[key]; dup {
			continue
		}
		t.keys[key] = struct{}{}
		t.cache.pending[key]++
	}
}

// release 释放全部被跟踪的键（每个键的计数减一，降到 0 才移除）。
func (t *pendingTracker) release() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cache.pendingMu.Lock()
	defer t.cache.pendingMu.Unlock()

	for key := range t.keys {
		if n, ok := t.cache.pending[key]; ok {
			if n <= 1 {
				delete(t.cache.pending, key)
			} else {
				t.cache.pending[key] = n - 1
			}
		}
	}
}

// markPending 把缓存键标记为进行中，返回可追加的跟踪器。
func (c *LocalImageCache) markPending(keys []string) *pendingTracker {
	t := &pendingTracker{cache: c, keys: make(map[string]struct{}, len(keys))}

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[string]int, len(keys))
	}
	c.pendingMu.Unlock()

	t.add(keys...)
	return t
}

// isPending 判断缓存键是否正在被本进程处理。
func (c *LocalImageCache) isPending(key string) bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return c.pending[key] > 0
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
//
// 返回的 error 只表示登记失败（数据库不可用等）。那种情况下不能把清洗
// 结果写进正文——正文不重算，写进去这批图片就永久丢了。
func (c *LocalImageCache) Cache(ctx context.Context, entryID uint64, urls []string) (map[string]string, error) {
	out := make(map[string]string)
	if len(urls) == 0 {
		return out, nil
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

	// 解析开始前就标记 URL 键；下载得到内容哈希后会追加标记。
	release := c.markPending(keysOf(pending))
	defer release.release()

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

			// 内容键一拿到就标记，而不是等整批 wg.Wait() 之后。
			//
			// 否则存在一个窗口：先完成的图片已经有内容键与文件，
			// 但还没进 pending；此时若同批其他图片还在下载（可能持续
			// 几十秒），Sweep 会把这张图当成孤儿删掉，随后 register
			// 拿着已删除的文件名写库，形成死链。
			release.add(res.cacheKey)

			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}(src)
	}
	wg.Wait()

	if len(results) == 0 {
		return out, nil
	}

	if ctx.Err() != nil {
		return out, nil
	}

	registered := c.register(entryID, results)

	// 登记失败：图片行/来源/引用一行都没写进去。此时不能把清洗结果交给
	// 调用方，否则正文落库后不会再重算，这批图片就永久丢了。
	//
	// 区分“登记失败”与“图片确实下载不到”：后者 register 仍会返回非空
	// 映射（原图不存在是常态，不该阻止正文写入）。
	if c.db != nil && len(registered) == 0 {
		return nil, fmt.Errorf("图片登记失败，本批 %d 张未写回正文", len(results))
	}

	for _, res := range results {
		// 只把已确认在库的图片写回正文。
		//
		// 这是必要条件：正文一旦写入就不会重算（fillEntryContent 只处理
		// 空正文），所以这里写进去的 URL 就是永久链接。若登记失败仍写
		// 站内 URL，文件不在数据库里，Sweep 过一段时间会把它当孤儿删掉，
		// 图片变成永久 404 且无任何自愈路径。
		//
		// 文件名必须取自 register 的返回值，不能用自己的 res.filename：
		// register 可能按内容去重（同批多条同内容只保留一条），也可能把
		// 名字归一化成库里已有的旧名。用 res.filename 就会输出一个没有
		// 数据库记录的文件，同样会变成永久 404。
		//
		// 未登记的 URL 直接 omit：调用方会把对应的 <img> 整体移除，
		// 比留下一个必然坏掉的链接好。
		filename := res.filename
		if c.db != nil {
			registeredName, ok := registered[res.cacheKey]
			if !ok {
				logger.Warn("[rss] 图片未登记，跳过站内链接",
					"url", res.sourceURL, "cache_key", res.cacheKey)
				continue
			}
			filename = registeredName
		}
		out[res.sourceURL] = c.publicPrefix + filename
	}
	return out, nil
}

// resolve 按"URL 映射 → 下载并算内容哈希 → 按内容复用"的顺序解析。
//
// 分两步而不是一步的原因：去重键是**内容**哈希，而抓取时手里只有 URL。
// 必须先下载一次才能知道内容；而 URL → 内容的映射存下来之后，同一个
// URL 第二轮就能直接命中，不必重复下载（时效签名 URL 除外——它每次
// 都变，但那正是需要内容去重来吸收的场景）。
func (c *LocalImageCache) resolve(ctx context.Context, src string, maxBytes int64, allowPrivate bool) (*imageResult, error) {
	urlKey := sha256Hex(src)

	// 0. 进程内映射：无数据库时的唯一复用手段。
	//
	// 命中后仍要 stat：Sweep 可能已删掉文件，只信内存会返回死链。
	if e, ok := c.memLookup(urlKey); ok {
		if info, statErr := os.Stat(filepath.Join(c.dir, e.filename)); statErr == nil && info.Size() > 0 {
			return &imageResult{
				sourceURL:   src,
				cacheKey:    e.cacheKey,
				filename:    e.filename,
				urlKey:      urlKey,
				contentType: e.contentType,
				sizeBytes:   info.Size(),
				sha256:      e.cacheKey,
			}, nil
		}
		c.memForget(urlKey)
	}

	// 1. 这个 URL 以前解析过：直接复用它指向的内容。
	if c.db != nil {
		var source model.RSSImageSource
		err := c.db.Where("url_key = ?", urlKey).First(&source).Error
		if err == nil {
			var img model.RSSImage
			if imgErr := c.db.First(&img, source.ImageID).Error; imgErr == nil {
				if info, statErr := os.Stat(filepath.Join(c.dir, img.Filename)); statErr == nil && info.Size() > 0 {
					return &imageResult{
						sourceURL:   src,
						cacheKey:    img.CacheKey,
						filename:    img.Filename,
						urlKey:      urlKey,
						contentType: img.ContentType,
						sizeBytes:   info.Size(),
						sha256:      img.CacheKey,
					}, nil
				}
				// 记录在但文件丢了：不信任这份映射，走重新下载。
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("查询图片来源失败: %w", err)
		}

		// 2. 兼容历史数据：旧版本按 md5(URL) 命名文件且没有来源表。
		//    命中就当场认领，顺便补建映射，避免这一代数据在升级后被重下。
		if name, info := c.findCachedFile(md5Hex(src)); name != "" {
			return &imageResult{
				sourceURL:   src,
				cacheKey:    md5Hex(src),
				filename:    name,
				urlKey:      urlKey,
				contentType: contentTypeForExt(filepath.Ext(name)),
				sizeBytes:   info.Size(),
			}, nil
		}
	}

	// 3. 没命中：下载到临时文件，算内容哈希后再决定落盘还是复用。
	return c.download(ctx, src, urlKey, maxBytes, allowPrivate)
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

// download 下载单张图片，按**内容哈希**去重后原子落盘。
//
// 关键点一：先用 maxBytes+1 读取，超过上限立即失败并删除临时文件。
// 旧实现用 io.LimitReader(body, 2MB) 直接写目标文件，超过 2MB 的图片
// 会被截断却仍被当作成功，磁盘上留下无法解码的坏文件。
//
// 关键点二：文件名在下载完成后才确定。唯一性由内容哈希保证，文件名
// 里的时间戳与随机段只是让文件不可枚举、不互相覆盖；两段职责分开，
// 所以同一张图不会被存成两份。
func (c *LocalImageCache) download(ctx context.Context, src, urlKey string, maxBytes int64, allowPrivate bool) (*imageResult, error) {
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

	// 内容哈希要到下载完成才知道，此后才能判断“这份内容是否已经存在”。
	contentHash := hex.EncodeToString(hasher.Sum(nil))

	// 第一道查重：数据库。放在锁外，因为这是网络/磁盘 IO，
	// 持锁会把它变成全局串行点。
	//
	// 这个查询能命中“其他条目/其他进程已经存过这份内容”的情况，
	// 是跨进程去重的主要途径。
	if existing, ok := c.lookupByContent(contentHash); ok {
		c.memRemember(urlKey, memEntry{
			filename:    existing.filename,
			cacheKey:    contentHash,
			contentType: existing.contentType,
		})
		c.memRememberContent(contentHash, memEntry{
			filename:    existing.filename,
			cacheKey:    contentHash,
			contentType: existing.contentType,
		})
		return &imageResult{
			sourceURL:   src,
			cacheKey:    contentHash,
			filename:    existing.filename,
			urlKey:      urlKey,
			contentType: existing.contentType,
			sizeBytes:   existing.sizeBytes,
			sha256:      contentHash,
		}, nil
	}

	// 第二道查重：进程内。同样在锁外——这是纯内存查询，
	// 若命中就无需进锁。
	if e, ok := c.memLookupContent(contentHash); ok {
		if info, statErr := os.Stat(filepath.Join(c.dir, e.filename)); statErr == nil && info.Size() > 0 {
			c.memRemember(urlKey, e)
			return &imageResult{
				sourceURL:   src,
				cacheKey:    contentHash,
				filename:    e.filename,
				urlKey:      urlKey,
				contentType: e.contentType,
				sizeBytes:   info.Size(),
				sha256:      contentHash,
			}, nil
		}
		c.memForgetContent(contentHash)
	}

	// 第三道：进锁后**再查一次内存**，然后落盘。
	//
	// 为什么必须进锁：并发下载同一内容（不同 URL、相同字节）时，
	// 两个 goroutine 会在前面两道查重里同时 miss（前一个刚落盘、
	// 但还没被这个 goroutine 看到），于是各自落一份文件。
	//
	// 为什么锁内只查内存不查库：锁外已经查过库；并发窗口内新落盘的
	// 记录会先写 memContent（register 要等整批），所以查内存就够。
	// 锁内如果再做 db 查询，所有图片下载都会被数据库延迟串行化。
	c.contentMu.Lock()
	defer c.contentMu.Unlock()

	if e, ok := c.memLookupContent(contentHash); ok {
		if info, statErr := os.Stat(filepath.Join(c.dir, e.filename)); statErr == nil && info.Size() > 0 {
			c.memRemember(urlKey, e)
			return &imageResult{
				sourceURL:   src,
				cacheKey:    contentHash,
				filename:    e.filename,
				urlKey:      urlKey,
				contentType: e.contentType,
				sizeBytes:   info.Size(),
				sha256:      contentHash,
			}, nil
		}
		c.memForgetContent(contentHash)
	}

	// 全新内容落盘。文件名 = rss_<时间戳>_<随机>_<内容哈希前8>.<ext>：
	// 时间戳与随机保证不可枚举且不同内容不会撞名，哈希后缀便于人工排障时
	// 从文件名反查是哪份内容。
	filename := imageFilename(contentHash, ext, time.Now())
	finalPath := filepath.Join(c.dir, filename)

	// 先写临时文件再 rename，避免并发读到写了一半的图片。
	if err := os.Rename(tmpPath, finalPath); err != nil {
		// rename 失败可能是目标已被并发写入；只要目标有效就复用。
		if info, statErr := os.Stat(finalPath); statErr == nil && info.Size() > 0 {
			e := memEntry{
				filename:    filename,
				cacheKey:    contentHash,
				contentType: contentType,
			}
			c.memRemember(urlKey, e)
			c.memRememberContent(contentHash, e)
			return &imageResult{
				sourceURL:   src,
				cacheKey:    contentHash,
				filename:    filename,
				urlKey:      urlKey,
				contentType: contentType,
				sizeBytes:   info.Size(),
				sha256:      contentHash,
			}, nil
		}
		return nil, fmt.Errorf("保存图片失败: %w", err)
	}

	e := memEntry{
		filename:    filename,
		cacheKey:    contentHash,
		contentType: contentType,
	}
	c.memRemember(urlKey, e)
	// 记到内容维度：同一批里后续解析到同内容的 URL 会直接命中，
	// 不会落第二份文件。
	c.memRememberContent(contentHash, e)

	return &imageResult{
		sourceURL:   src,
		cacheKey:    contentHash,
		filename:    filename,
		urlKey:      urlKey,
		contentType: contentType,
		sizeBytes:   n,
		sha256:      contentHash,
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

		// 身份是**内容**，与下载路径保持一致。
		//
		// 哈希始终以磁盘文件为准，不信任入参：extension 是独立进程，
		// 传空串、传错值都是可能的。信任错误哈希会让这条记录永远无法
		// 被正常下载路径命中（每次 download 都 miss → 重复落盘），
		// 更糟的是若这个错键恰好等于另一张图的 cache_key，
		// upsert 会把那条记录改指到本次文件，那张图在所有条目里变成错图。
		filePath := filepath.Join(c.dir, item.Filename)
		contentHash := fileSHA256(filePath)
		if contentHash == "" {
			continue // 读不出内容就不能登记，避免污染唯一键
		}
		if declared := normalizeSHA256(item.SHA256); declared != "" && declared != contentHash {
			logger.Warn("[rss] Adopt 传入的内容哈希与实际不符，以文件为准",
				"file", item.Filename, "declared", declared, "actual", contentHash)
		}

		results = append(results, &imageResult{
			sourceURL:   item.SourceURL,
			cacheKey:    contentHash,
			filename:    item.Filename,
			urlKey:      sha256Hex(item.SourceURL),
			contentType: contentType,
			// 大小以实际文件为准：入参可能过时或伪造。
			sizeBytes: info.Size(),
			sha256:    contentHash,
		})
	}

	if len(results) > 0 {
		registered := c.register(entryID, results)
		// 必须在 register 之后再构造返回值，并且用 register 给的权威文件名。
		//
		// 两个理由：
		//   - register 会按内容去重：同批多个 item 内容相同时只有一条入库，
		//     被丢弃的那条用自己的 item.Filename 就指向一个没有记录的
		//     文件，Sweep 会把它当历史遗留删掉 → 延时 404；
		//   - 旧文件仍在时，文件名会被归一化成库里已有的旧名。
		for _, res := range results {
			filename := res.filename
			if c.db != nil {
				registeredName, ok := registered[res.cacheKey]
				if !ok {
					continue
				}
				filename = registeredName
			}
			out[res.sourceURL] = c.publicPrefix + filename
		}
	}
	return out
}

// register 把解析结果写入资源表，并登记 URL 映射与条目引用。
//
// 两层登记：
//   - rss_images：一份内容一行（按 CacheKey 唯一）；
//   - rss_image_sources：一个 URL 一行，指向它提供的内容。
//
// 不登记 URL 映射的话，每轮抓取都得重新下载才能算出内容哈希，
// 去重反而变成了性能倒退。
//
// 返回 cacheKey → **已登记的文件名**。调用方必须按这个映射输出 URL：
// 不能用自己的 res.filename，原因有两个：
//   - register 内部会按内容去重（同批多个 URL 解析到同一内容时，只有一条
//     会入库），被丢弃的那条从未被归一化；
//   - 旧文件仍在时，filename 会被归一化成库里已在的旧名。
//
// 两者任一遗漏，调用方都会输出一个没有数据库记录的文件名，而正文写入后
// 不会重算（fillEntryContent 只处理空正文），那个链接就永久 404。
//
// 所有写入在一个事务里：图片行、URL 映射、条目引用要么全部提交，要么
// 全部回滚。部分提交（如图片行成功、引用失败）会让文件失去 30 天保护，
// 却没有自愈路径。
func (c *LocalImageCache) register(entryID uint64, results []*imageResult) map[string]string {
	if c.db == nil || len(results) == 0 {
		return nil
	}

	// 按内容哈希去重，这一步不能省。
	//
	// results 来自一批 URL，其中多个不同 URL 完全可能解析到同一内容
	// （CDN 签名参数、http/https 双写、转载站）。下面是一条多行
	// INSERT ... ON CONFLICT DO UPDATE，而 PostgreSQL 不允许同一语句内
	// 两行命中同一个冲突键：
	//
	//	21000: ON CONFLICT DO UPDATE command cannot affect row a second time
	//
	// 后果不是丢一行，而是**整批登记失败**：没有 image、没有 source、
	// 没有 ref。这与条目路径上的 entriesFromItems 是同一个坑。
	results = dedupeResultsByContent(results)

	now := time.Now()
	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	// 先查已有记录，决定 filename 该不该被本次覆盖。
	//
	// 这里有个必须处理的矛盾：
	//   - 并发场景下，后到者走的是“按内容命中”，临时文件已删，
	//     绝不能把 filename 改成它的（否则指向不存在的文件）；
	//   - 但“记录在、文件丢了”（磁盘被清、手动删、迁移漏拷）时，
	//     又必须把 filename 改成本次新落盘的文件，否则永远死链：
	//     下次 resolve 查到旧 filename → stat 失败 → 重下载 → 又写不进去。
	//
	// 区分依据只能是磁盘：旧文件名对应的文件还在 → 沿用旧名；
	// 不在了 → 用本次的名字。
	prevByKey := make(map[string]model.RSSImage, len(results))
	keys := make([]string, 0, len(results))
	for _, res := range results {
		if res.cacheKey != "" {
			keys = append(keys, res.cacheKey)
		}
	}
	if len(keys) > 0 {
		var prev []model.RSSImage
		if err := c.db.Where("cache_key IN ?", keys).Find(&prev).Error; err != nil {
			// 不能静默：查不到历史记录时下面会一律用新文件名覆盖，
			// 并发场景下可能把已有有效记录改到另一份文件上。
			// 降级是安全的（只是可能多一份孤儿文件，由 Sweep 收拾），
			// 但必须留下痕迹。
			logger.Warn("[rss] 查询已有图片记录失败，将按新文件登记", "error", err)
		} else {
			for _, img := range prev {
				prevByKey[img.CacheKey] = img
			}
		}
	}

	// registered 是给调用方的权威结果：cacheKey → 实际会写进库的文件名。
	// 不直接改 res.filename 是因为调用方可能拿到未去重的原始 results。
	registered := make(map[string]string, len(results))
	rows := make([]model.RSSImage, 0, len(results))
	keys = keys[:0]
	for _, res := range results {
		if res.cacheKey == "" {
			continue
		}
		filename := res.filename
		if old, ok := prevByKey[res.cacheKey]; ok && old.Filename != "" {
			if info, statErr := os.Stat(filepath.Join(c.dir, old.Filename)); statErr == nil && info.Size() > 0 {
				// 旧文件还在：保留旧名。
				filename = old.Filename
			}
		}
		rows = append(rows, model.RSSImage{
			CacheKey:  res.cacheKey,
			SourceURL: res.sourceURL,
			Filename:  filename,
			// Content-Type 来自外部响应头，长度不受控，而列是 varchar(100)。
			// 不裁剪会让这一行被 PostgreSQL 拒收，而写入是一条多行 INSERT
			// —— 一条坏数据带走整批（与条目/日志路径同类的坑）。
			ContentType: model.TruncateRunes(res.contentType, 100),
			SizeBytes:   res.sizeBytes,
			LastSeenAt:  now,
		})
		registered[res.cacheKey] = filename
		keys = append(keys, res.cacheKey)
	}
	if len(rows) == 0 {
		return nil
	}

	// 一个事务包住全部写入。
	//
	// 分开写会出现部分提交：图片行已入库、引用行失败。那时文件存在但
	// 没有引用保护，Sweep 会在宽限期后把它当孤儿删掉，而正文里已经
	// 写了站内 URL（正文不重算）——永久 404，且无自愈路径。
	// 事务回滚后调用方不写 URL，该条目正文保持为空，下次抓取会重试。
	err := c.db.Transaction(func(tx *gorm.DB) error {
		// 冲突时更新 filename：上面的判断保证它要么是“文件仍在的旧名”，
		// 要么是“本次新落盘的名字”，两种都指向真实存在的文件。
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "cache_key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"filename", "content_type", "size_bytes", "last_seen_at", "updated_at",
			}),
		}).Create(&rows).Error; err != nil {
			return fmt.Errorf("写入图片资源失败: %w", err)
		}

		var stored []model.RSSImage
		if err := tx.Where("cache_key IN ?", keys).Find(&stored).Error; err != nil {
			return fmt.Errorf("查询图片资源失败: %w", err)
		}
		idByKey := make(map[string]uint64, len(stored))
		for _, img := range stored {
			idByKey[img.CacheKey] = img.ID
		}

		// 登记 URL → 内容 的映射。
		//
		// 同一个 URL 换了内容（源站替换了图）：保留旧映射会让后续抓取
		// 永远拿到旧文件，因此冲突时更新。
		//
		// 同样需要按 urlKey 去重：同一批里重复 URL 会让这条多行 upsert
		// 撞上同一个冲突键（21000）。
		seenURL := make(map[string]struct{}, len(results))
		sources := make([]model.RSSImageSource, 0, len(results))
		for _, res := range results {
			if res.urlKey == "" {
				continue
			}
			if _, dup := seenURL[res.urlKey]; dup {
				continue
			}
			seenURL[res.urlKey] = struct{}{}
			id, ok := idByKey[res.cacheKey]
			if !ok {
				continue
			}
			sources = append(sources, model.RSSImageSource{
				ImageID:   id,
				URLKey:    res.urlKey,
				SourceURL: res.sourceURL,
			})
		}
		if len(sources) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "url_key"}},
				DoUpdates: clause.AssignmentColumns([]string{"image_id", "source_url", "updated_at"}),
			}).Create(&sources).Error; err != nil {
				return fmt.Errorf("登记图片来源失败: %w", err)
			}
		}

		// 没有条目 ID 时无法建立引用（条目还没入库），只登记资源本身。
		if entryID == 0 {
			return nil
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
			return nil
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&refs).Error; err != nil {
			return fmt.Errorf("登记图片引用失败: %w", err)
		}
		return nil
	})
	if err != nil {
		logger.Warn("[rss] 登记图片失败，本批不写回正文（下次抓取会重试）", "error", err)
		return nil
	}

	return registered
}

// dedupeResultsByContent 按内容哈希去重，保留磁盘上真实存在的那一份。
//
// 为什么需要：一批 URL 里多个不同 URL 完全可能解析到同一内容。
// 不去重就会让 register 的多行 upsert 撞上同一个冲突键。
//
// 保留策略：优先保留文件真实存在的那份（通常是先落盘者），
// 其余只丢弃结果记录——它们的临时文件本来就没创建（走“按内容命中”
// 分支时临时文件已删），所以不需要额外清理。
func dedupeResultsByContent(results []*imageResult) []*imageResult {
	if len(results) < 2 {
		return results
	}
	seen := make(map[string]int, len(results))
	out := make([]*imageResult, 0, len(results))
	for _, res := range results {
		if res == nil || res.cacheKey == "" {
			continue
		}
		idx, dup := seen[res.cacheKey]
		if !dup {
			seen[res.cacheKey] = len(out)
			out = append(out, res)
			continue
		}
		// 重复内容：保留文件名更“实”的那份（非空），其余丢弃。
		if out[idx].filename == "" && res.filename != "" {
			out[idx] = res
		}
	}
	return out
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
		Where("NOT EXISTS (SELECT 1 FROM rss_image_refs WHERE rss_image_refs.image_id = rss_images.id)").
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
		// 先删 URL 映射，再删内容记录。
		//
		// 顺序反了（或漏删）会让 rss_image_sources 留下指向已删
		// 图片的悬挂行。功能上不至于崩溃（resolve 查不到图片会重下），
		// 但这些行会永久累积，而它们指向的 image_id 永远不会再被创建。
		if err := c.db.Unscoped().
			Where("image_id = ?", img.ID).
			Delete(&model.RSSImageSource{}).Error; err != nil {
			logger.Warn("[rss] 删除图片 URL 映射失败", "id", img.ID, "error", err)
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

// keysOf 计算一批源 URL 的跟踪键。
//
// 用的是 URL 哈希而不是内容哈希：调用时图片还没下载，只有 URL。
func keysOf(urls []string) []string {
	keys := make([]string, 0, len(urls))
	for _, u := range urls {
		keys = append(keys, sha256Hex(u))
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
		Where("NOT EXISTS (SELECT 1 FROM rss_entries e WHERE e.id = rss_image_refs.entry_id)").
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

// sha256Hex 计算字符串的 SHA256 十六进制。
//
// 用于两个不同用途：图片内容身份（CacheKey）、源 URL 身份（URLKey）。
// 两者都是“找个稳定的搜索键”而非密码学用途，但用 SHA256 可以避免
// 大型图床场景下 md5 的碰撞担忧，也与已有内容哈希字段一致。
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// sha256BytesHex 计算字节内容的 SHA256 十六进制。
func sha256BytesHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fileSHA256 计算文件的 SHA256 十六进制；失败返回空串。
func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeSHA256 把外部传入的 SHA256 规范化；不像合法哈希时返回空串。
//
// 不能直接信任调用方：extension 是独立进程，传空串、传 md5、传带前缀
// 的值都是可能的。写进唯一键之前必须先验。
func normalizeSHA256(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return s
}

// randomHex 返回 n 字节的密码学随机十六进制串。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// 熵源不可用时回退到纳秒时间戳：宁愿可预测，也不能碰撞。
		// 文件名本身不承担安全职责（内容是公开的图片），
		// 唯一性由内容哈希与随机段共同保证。
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// imageFilename 生成缓存文件名：`rss_<时间戳>_<随机>_<内容哈希前8>.<ext>`。
//
// 时间戳与随机段负责“不可枚举、不随时间重复”，哈希后级负责“人工排障
// 时能从文件名认出是哪份内容”。真正的唯一性由数据库的 CacheKey 保证，
// 不依赖文件名——这是为什么两者要分开。
func imageFilename(contentHash, ext string, now time.Time) string {
	short := contentHash
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		short = "nohash"
	}
	return fmt.Sprintf("rss_%s_%s_%s%s",
		strconv.FormatInt(now.Unix(), 36), randomHex(4), short, ext)
}

// cachedContent 是“按内容找到的已有图片”。
type cachedContent struct {
	filename    string
	contentType string
	sizeBytes   int64
}

// lookupByContent 按内容哈希查找已经落盘且可用的图片。
//
// 这是“避免重复”的关键：下载完才知道内容哈希，此后才能判断这份内容
// 是否已经存在。记录在但文件丢了的情况返回 false，让调用方重新落盘，
// 不信任失效的元数据。
func (c *LocalImageCache) lookupByContent(contentHash string) (cachedContent, bool) {
	if c.db == nil || contentHash == "" {
		return cachedContent{}, false
	}

	var img model.RSSImage
	if err := c.db.Where("cache_key = ?", contentHash).First(&img).Error; err != nil {
		return cachedContent{}, false
	}
	info, err := os.Stat(filepath.Join(c.dir, img.Filename))
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return cachedContent{}, false
	}
	return cachedContent{
		filename:    img.Filename,
		contentType: img.ContentType,
		sizeBytes:   info.Size(),
	}, true
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
