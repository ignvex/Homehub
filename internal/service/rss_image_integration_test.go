package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 本文件是图片缓存的真实数据库端到端验证，只在设置 HOMEHUB_TEST_DB 时运行。
//
// 为什么必须要有它：仓库里所有关于去重的断言都基于 sqlmock，而 sqlmock
// 只能证明"生成了什么 SQL"。以下问题只有真实 PostgreSQL 能回答：
//
//   - 多行 ON CONFLICT 撞键是否真的整批失败（已由 database 包的集成测试证实）
//   - 不同 URL 解析到同一内容时，磁盘上是否真的只有一份文件
//   - rss_image_sources 的 url_key 唯一约束是否真的生效
//   - Sweep 是否真的不会误删刚登记的文件
//
// 运行方式：
//
//	HOMEHUB_TEST_DB="host=192.168.1.2 user=test password=xxx dbname=testdb sslmode=disable" \
//	  go test ./internal/service/ -run IntegrationImage -v

// integrationSchema 是图片集成测试专用的 schema。
//
// 为什么要独立 schema：`go test ./...` 会**并行**跑不同包，而 database 包的
// 集成测试会在同一个库里 DROP/CREATE 本仓库的表。两者同时作用于
// public 时，一边正在插入、另一边把表删了，表现为随机失败。
// 用独立 schema 彻底隔开，同时顺带验证了“表不在 public 下也能工作”。
const integrationSchema = "it_service"

// imageIntegrationDB 连接真实数据库并在独立 schema 里准备图片相关表。
//
// 不经过 database.Init：那个函数用 sync.Once 保证全局单例，
// 多个测试共享同一实例会互相干扰（且无法从其他包重置）。
func imageIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	// 先用默认连接建 schema（search_path 不能在 schema 不存在时生效）。
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	if err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + integrationSchema).Error; err != nil {
		t.Fatalf("创建测试 schema 失败: %v", err)
	}
	if sqlDB, err := admin.DB(); err == nil {
		sqlDB.Close()
	}

	db, err := gorm.Open(postgres.Open(dsn+" search_path="+integrationSchema+",public"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.Close()
		// 只删自己的 schema。
		if cleanupDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard}); err == nil {
			cleanupDB.Exec("DROP SCHEMA IF EXISTS " + integrationSchema + " CASCADE")
			if s, err := cleanupDB.DB(); err == nil {
				s.Close()
			}
		}
	})

	if err := db.AutoMigrate(&model.RSSImage{}, &model.RSSImageSource{}, &model.RSSImageRef{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	// 每次从干净状态开始。
	//
	// 同样必须显式带 schema：search_path 里有 public 兜底，
	// 裸写 `DELETE FROM rss_images` 在测试表未建成时会打到 public 的
	// 同名表上，把真实数据删掉。
	for _, table := range []string{"rss_image_refs", "rss_image_sources", "rss_images"} {
		db.Exec("DELETE FROM " + integrationSchema + "." + table)
	}

	return db
}

// nowTime 返回当前时间，供模型字面量使用。
func nowTime() time.Time { return time.Now() }

// TestIntegrationImageDedupesByContent 验证核心承诺：
// 两个不同 URL 返回相同字节时，只落一份文件、只存一条内容记录，
// 但两个 URL 都要能反查到它。
func TestIntegrationImageDedupesByContent(t *testing.T) {
	db := imageIntegrationDB(t)

	payload := []byte("\x89PNG\r\n\x1a\nidentical-image-bytes")

	// 两个不同路径返回同一份内容。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)
	dir := t.TempDir()

	cache := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}

	// 同一批里两个 URL → 同内容。
	batchA, err := cache.Cache(context.Background(), 1, []string{srv.URL + "/a.png"})
	if err != nil {
		t.Fatalf("Cache 失败: %v", err)
	}
	batchB, err := cache.Cache(context.Background(), 2, []string{srv.URL + "/b.png"})
	if err != nil {
		t.Fatalf("Cache 失败: %v", err)
	}

	if len(batchA) != 1 || len(batchB) != 1 {
		t.Fatalf("两批各应返回 1 个映射，实际 %v / %v", batchA, batchB)
	}

	// 关键断言一：两个 URL 指向同一个站内文件。
	var urlA, urlB string
	for _, v := range batchA {
		urlA = v
	}
	for _, v := range batchB {
		urlB = v
	}
	if urlA != urlB {
		t.Errorf("同内容被存成两份: %q vs %q", urlA, urlB)
	}

	// 关键断言二：磁盘上只有一个文件。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, e := range entries {
		if !e.IsDir() {
			files++
		}
	}
	if files != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录里有 %d 个文件，期望 1: %v", files, names)
	}

	// 关键断言三：rss_images 只有一条，rss_image_sources 有两条。
	var imageCount int64
	db.Model(&model.RSSImage{}).Count(&imageCount)
	if imageCount != 1 {
		t.Errorf("rss_images 记录数 = %d，期望 1", imageCount)
	}

	var sourceCount int64
	db.Model(&model.RSSImageSource{}).Count(&sourceCount)
	if sourceCount != 2 {
		t.Errorf("rss_image_sources 记录数 = %d，期望 2（两个不同 URL）", sourceCount)
	}

	// 关键断言四：cache_key 是内容哈希，与文件内容一致。
	var img model.RSSImage
	if err := db.First(&img).Error; err != nil {
		t.Fatal(err)
	}
	wantHash := sha256BytesHex(payload)
	if img.CacheKey != wantHash {
		t.Errorf("cache_key = %q，期望内容哈希 %q", img.CacheKey, wantHash)
	}

	// 关键断言五：文件名符合 rss_<时间戳>_<随机>_<哈希8>.<ext>。
	if !strings.HasPrefix(img.Filename, "rss_") {
		t.Errorf("文件名缺少 rss_ 前缀: %q", img.Filename)
	}
	if !strings.HasSuffix(strings.TrimSuffix(img.Filename, ".png"), wantHash[:8]) {
		t.Errorf("文件名应含内容哈希前8位: %q", img.Filename)
	}
}

// TestIntegrationImageSourcesUniqueURLKey 验证 url_key 唯一约束真实生效。
//
// 这条保证"一个 URL 只对应一份内容"，也是 resolve 快速反查的前提。
func TestIntegrationImageSourcesUniqueURLKey(t *testing.T) {
	db := imageIntegrationDB(t)

	img := model.RSSImage{
		CacheKey:    strings.Repeat("a", 64),
		SourceURL:   "https://example.com/1.png",
		Filename:    "rss_x_y_aaaaaaaa.png",
		ContentType: "image/png",
		SizeBytes:   10,
		LastSeenAt:  nowTime(),
	}
	if err := db.Create(&img).Error; err != nil {
		t.Fatalf("创建图片记录失败: %v", err)
	}

	src := model.RSSImageSource{
		ImageID:   img.ID,
		URLKey:    strings.Repeat("b", 64),
		SourceURL: "https://example.com/1.png",
	}
	if err := db.Create(&src).Error; err != nil {
		t.Fatalf("创建来源记录失败: %v", err)
	}

	dup := model.RSSImageSource{
		ImageID:   img.ID,
		URLKey:    strings.Repeat("b", 64), // 同一个 url_key
		SourceURL: "https://example.com/other.png",
	}
	err := db.Create(&dup).Error
	if err == nil {
		t.Fatal("重复 url_key 竟然插入成功；唯一约束没有生效，反查会拿到不确定的结果")
	}
	if !strings.Contains(err.Error(), "duplicate key") && !strings.Contains(err.Error(), "23505") {
		t.Errorf("失败原因不是唯一约束冲突: %v", err)
	}
}

// TestIntegrationImageFilenameTooLongForColumn 验证过长的 Content-Type 会被裁剪。
//
// 列是 varchar(100)，而 Content-Type 来自外部响应头。不裁剪会让这条
// 多行 INSERT 整批被拒收（与条目/日志路径同类的坑）。
func TestIntegrationImageFilenameTooLongForColumn(t *testing.T) {
	db := imageIntegrationDB(t)

	longType := "image/" + strings.Repeat("x", 300)
	data := []byte("\x89PNG\r\n\x1a\nbytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", longType)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)
	cache := &LocalImageCache{db: db, dir: t.TempDir(), publicPrefix: imageURLPrefix}

	if _, err := cache.Cache(context.Background(), 1, []string{srv.URL + "/x.png"}); err != nil {
		t.Fatalf("Cache 失败（超长 Content-Type 应被裁剪而不是导致整批失败）: %v", err)
	}

	var img model.RSSImage
	if err := db.First(&img).Error; err != nil {
		t.Fatalf("图片未登记: %v", err)
	}
	if len([]rune(img.ContentType)) > 100 {
		t.Errorf("content_type 长度 %d 超过 varchar(100)", len([]rune(img.ContentType)))
	}
}

// TestIntegrationSweepKeepsRegisteredImage 验证 Sweep 不会删掉刚登记且有引用的图片。
func TestIntegrationSweepKeepsRegisteredImage(t *testing.T) {
	db := imageIntegrationDB(t)

	payload := []byte("\x89PNG\r\n\x1a\nkeep-me")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576,
		"image_cleanup_enabled": true, "image_retention_days": 30}`)
	dir := t.TempDir()
	cache := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}

	out, err := cache.Cache(context.Background(), 42, []string{srv.URL + "/k.png"})
	if err != nil || len(out) != 1 {
		t.Fatalf("Cache 失败: %v %v", out, err)
	}

	// 立即清理（grace=0）：有引用的图片必须保留。
	if err := cache.Sweep(context.Background(), 0); err != nil {
		t.Fatalf("Sweep 失败: %v", err)
	}

	var img model.RSSImage
	if err := db.First(&img).Error; err != nil {
		t.Fatalf("有引用的图片被删除了: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, img.Filename)); statErr != nil {
		t.Errorf("有引用的图片文件被删除了: %v", statErr)
	}
}
