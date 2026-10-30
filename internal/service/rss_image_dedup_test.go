package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// rssImageColumns 是 RSSImage 查询所需的列（顺序需与模型一致）。
func rssImageColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "deleted_at",
		"cache_key", "source_url", "filename", "content_type",
		"size_bytes", "last_seen_at",
	}
}

// TestDownloadReusesExistingContentForDifferentURL 锁住"避免重复"的核心行为。
//
// 同一张图片常以多个 URL 出现：CDN 签名参数不同、转载站换了域名、
// 时效 token 每轮都变。身份必须是**内容**而不是 URL，否则同一张图会被
// 反复下载、在磁盘上存成多份，而图床/OSS 的存储成本就这样涨上去。
//
// 用例：库里已有内容 "IMG"（文件名 existing.png）；此时从另一个 URL
// 下载到同样字节——必须复用已有文件，且目录里不能多出第二个文件。
func TestDownloadReusesExistingContentForDifferentURL(t *testing.T) {
	dir := t.TempDir()

	const payload = "\x89PNG\r\n\x1a\nsame-bytes"
	existing := "rss_old_aaaa_51dfc914.png"
	if err := os.WriteFile(filepath.Join(dir, existing), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	contentHash := sha256BytesHex([]byte(payload))

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// lookupByContent 按内容哈希查已有记录。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key = \$1.*LIMIT`).
		WithArgs(contentHash, 1).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "https://a/old.png", existing,
			"image/png", len(payload), nil,
		))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	newURL := srv.URL + "/copy.png"

	res, err := c.download(context.Background(), newURL, sha256Hex(newURL), 1<<20, true)
	if err != nil {
		t.Fatalf("download 失败: %v", err)
	}

	if res.filename != existing {
		t.Errorf("应复用已有文件 %q，实际生成 %q（同一内容被存成了两份）", existing, res.filename)
	}
	if res.cacheKey != contentHash {
		t.Errorf("cacheKey = %q, want 内容哈希 %q", res.cacheKey, contentHash)
	}

	// 目录里只能有一个文件：临时文件必须被清理掉。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files++
	}
	if files != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录中有 %d 个文件，期望 1（同内容被重复落盘）: %v", files, names)
	}

	// 内存映射应记住这个 URL，下次不必再下载。
	if e, ok := c.memLookup(sha256Hex(newURL)); !ok || e.filename != existing {
		t.Errorf("进程内映射未记录: %+v, want filename %q", e, existing)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestDownloadStoresNewContentWhenDifferent 确认内容不同时不会误复用。
//
// 反向用例：如果去重逻辑写错成"总是复用"，不同图片会互相覆盖，
// 这是比重复存储更严重的错误。
func TestDownloadStoresNewContentWhenDifferent(t *testing.T) {
	dir := t.TempDir()

	const existingPayload = "\x89PNG\r\n\x1a\nold"
	const newPayload = "\x89PNG\r\n\x1a\nbrand-new-image"

	existing := "rss_old_bbbb_11111111.png"
	if err := os.WriteFile(filepath.Join(dir, existing), []byte(existingPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// 按新内容的哈希查不到任何记录。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key = \$1.*LIMIT`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(newPayload))
	}))
	defer srv.Close()

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	url := srv.URL + "/new.png"

	res, err := c.download(context.Background(), url, sha256Hex(url), 1<<20, true)
	if err != nil {
		t.Fatalf("download 失败: %v", err)
	}

	if res.filename == existing {
		t.Fatalf("不同内容复用了旧文件 %q，会覆盖原图", existing)
	}
	if res.cacheKey != sha256BytesHex([]byte(newPayload)) {
		t.Errorf("cacheKey 不是新内容哈希: %q", res.cacheKey)
	}
	if _, err := os.Stat(filepath.Join(dir, res.filename)); err != nil {
		t.Errorf("新文件未落盘: %v", err)
	}
	// 旧文件必须保持原样。
	old, err := os.ReadFile(filepath.Join(dir, existing))
	if err != nil || string(old) != existingPayload {
		t.Errorf("旧文件被改动: %v %q", err, old)
	}
}

// TestMediaAssetSourceDefaultsToUpload 确认用户上传的资源带上 upload 标记。
//
// 图床与 RSS 抓取缓存分属两张表，来源标记是给统一管理视图用的：
// 将来如果要在同一个页面展示两类资源，靠它区分，而不是靠猜文件名。
func TestMediaAssetSourceDefaultsToUpload(t *testing.T) {
	a := model.MediaAsset{Source: MediaSourceUpload}
	if a.Source != "upload" {
		t.Errorf("Source = %q, want upload", a.Source)
	}
	if MediaSourceRSS == MediaSourceUpload {
		t.Error("rss 与 upload 标记不能相同，否则无法区分来源")
	}
}

// TestRegisterWritesURLMapping 确认登记时会写 URL → 内容 的映射。
//
// 少了这一步，去重会退化成"每轮都下载一遍才知道内容哈希"，
// 结果是省了磁盘却赔上了带宽，比不去重更糟。
func TestRegisterWritesURLMapping(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	contentHash := sha256BytesHex([]byte("img-bytes"))
	urlKey := sha256Hex("https://example.com/a.png")

	// 1. 前置查询：本次要登记的内容是否已有记录。
	//    返回空集表示全新内容，走“用本次文件名”。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))

	// 写入在一个事务里：图片行、URL 映射、引用要么全成要么全回滚。
	mock.ExpectBegin()

	// 2. 写入内容记录（upsert）。
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))

	// 2. 查回 ID。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			7, nil, nil, nil, contentHash, "https://example.com/a.png",
			"rss_ts_rand_aaaaaaaa.png", "image/png", 9, nil,
		))

	// 3. 写入 URL 映射 —— 本用例的核心断言。
	mock.ExpectQuery(`INSERT INTO "rss_image_sources".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	// 4. 写入条目引用。
	mock.ExpectQuery(`INSERT INTO "rss_image_refs"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	mock.ExpectCommit()

	c := &LocalImageCache{db: db, dir: t.TempDir(), publicPrefix: imageURLPrefix}
	c.register(42, []*imageResult{{
		sourceURL:   "https://example.com/a.png",
		cacheKey:    contentHash,
		filename:    "rss_ts_rand_aaaaaaaa.png",
		urlKey:      urlKey,
		contentType: "image/png",
		sizeBytes:   9,
		sha256:      contentHash,
	}})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足（URL 映射可能没写）: %v", err)
	}
}

// TestRegisterSkipsMappingWithoutURLKey 确认没有 URL 键时不写映射表。
//
// Adopt 与历史数据回填可能没有可用的 URL 键；此时写一行空键会占住
// 唯一索引，让后续所有真实 URL 都无法登记。
func TestRegisterSkipsMappingWithoutURLKey(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	contentHash := sha256BytesHex([]byte("img"))

	// 前置查询：无历史记录。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			8, nil, nil, nil, contentHash, "", "rss_ts_rand_bbbbbbbb.png",
			"image/png", 3, nil,
		))
	// 不应出现 INSERT INTO rss_image_sources，也不应有引用
	// （entryID=0 表示条目还没入库）。
	mock.ExpectCommit()

	c := &LocalImageCache{db: db, dir: t.TempDir(), publicPrefix: imageURLPrefix}
	c.register(0, []*imageResult{{
		sourceURL:   "",
		cacheKey:    contentHash,
		filename:    "rss_ts_rand_bbbbbbbb.png",
		urlKey:      "",
		contentType: "image/png",
		sizeBytes:   3,
		sha256:      contentHash,
	}})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestRegisterKeepsExistingFilenameWhenFileAlive 确认已有文件仍然存在时，
// 登记不会把记录改指到本次新下载的文件上。
//
// 否则同一份内容会在磁盘上留下两份：旧文件失去 DB 引用，被 Sweep 当成
// 历史遗留清掉；而数据库指向新文件——虽然最终可用，但等于白下载一次。
func TestRegisterKeepsExistingFilenameWhenFileAlive(t *testing.T) {
	dir := t.TempDir()
	const oldName = "rss_old_aaaa_11111111.png"
	if err := os.WriteFile(filepath.Join(dir, oldName), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	contentHash := sha256BytesHex([]byte("img"))

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "", oldName, "image/png", 3, nil,
		))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "", oldName, "image/png", 3, nil,
		))
	mock.ExpectCommit()

	res := &imageResult{
		cacheKey:  contentHash,
		filename:  "rss_new_bbbb_22222222.png",
		sizeBytes: 3,
		sha256:    contentHash,
	}
	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	registered := c.register(0, []*imageResult{res})

	// 断言 registered 给出的是库里实际登记的名字，而不是调用方自己的文件名。
	if got := registered[contentHash]; got != oldName {
		t.Errorf("旧文件仍在时应登记旧名 %q，实际登记 %q", oldName, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestRegisterReplacesFilenameWhenFileLost 锁住一条死链回归。
//
// 场景：DB 里有内容记录，但对应文件不见了（磁盘被清、迁移漏拷、
// 手动删除）。此时如果 upsert 不更新 filename，就会形成永久死链：
// 下次 resolve 查到旧文件名 → stat 失败 → 重新下载 → 又写不进去。
// 每轮抓取都白下载一次，页面上的图却始终是破的。
func TestRegisterReplacesFilenameWhenFileLost(t *testing.T) {
	dir := t.TempDir()
	const missingOld = "rss_gone_dddd_44444444.png"
	const newName = "rss_new_cccc_33333333.png"

	// 只有新文件，旧文件确实不存在。
	if err := os.WriteFile(filepath.Join(dir, newName), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	contentHash := sha256BytesHex([]byte("img"))

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// 前置查询：旧记录指向一个不存在的文件。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "", missingOld, "image/png", 3, nil,
		))
	// 关键断言：upsert 必须带上 filename 更新，否则记录永远指向缺失文件。
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "rss_images".*"filename"="excluded"\."filename"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "", newName, "image/png", 3, nil,
		))
	mock.ExpectCommit()

	res := &imageResult{
		cacheKey:  contentHash,
		filename:  newName,
		sizeBytes: 3,
		sha256:    contentHash,
	}
	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	registered := c.register(0, []*imageResult{res})

	if got := registered[contentHash]; got != newName {
		t.Errorf("旧文件已丢失时应登记新文件名 %q，实际 %q", newName, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足（filename 可能没被更新，会形成死链）: %v", err)
	}
}

// TestMemCacheHitKeepsContentKey 锁住一条静默失效回归。
//
// 进程内映射命中时必须同时给出 cacheKey。若只给文件名，register 会以
// "没有内容键"为由跳过整条登记：图片引用不会重建、last_seen_at 不再
// 更新——表现为新条目贴上来的图片随时可能被 Sweep 当成孤儿删掉，
// 而日志里没有任何错误。
func TestMemCacheHitKeepsContentKey(t *testing.T) {
	dir := t.TempDir()
	const filename = "rss_ts_rand_deadbeef.png"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	contentHash := sha256BytesHex([]byte("img"))
	const url = "https://example.com/hit.png"
	urlKey := sha256Hex(url)

	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}
	c.memRemember(urlKey, memEntry{
		filename:    filename,
		cacheKey:    contentHash,
		contentType: "image/png",
	})

	res, err := c.resolve(context.Background(), url, 1<<20, false)
	if err != nil {
		t.Fatalf("resolve 失败: %v", err)
	}
	if res.cacheKey != contentHash {
		t.Errorf("cacheKey = %q，期望 %q（为空会让 register 跳过登记）", res.cacheKey, contentHash)
	}
	if res.filename != filename {
		t.Errorf("filename = %q, want %q", res.filename, filename)
	}
	if res.sha256 != contentHash {
		t.Errorf("sha256 = %q, want %q", res.sha256, contentHash)
	}
}

// TestMemCacheHitFallsBackWhenFileGone 确认内存命中的文件被删后，
// 不会返回死链，而是回退到重新下载（这里无 db，故直接报错）。
func TestMemCacheHitFallsBackWhenFileGone(t *testing.T) {
	dir := t.TempDir()

	// 用本地 server 让“下载失败”即时返回，避免真实 DNS 超时拖慢测试。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	url := srv.URL + "/gone.png"
	urlKey := sha256Hex(url)

	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}
	c.memRemember(urlKey, memEntry{
		filename:    "rss_ts_rand_missing.png", // 文件并未创建
		cacheKey:    "deadbeef",
		contentType: "image/png",
	})

	if _, ok := c.memLookup(urlKey); !ok {
		t.Fatal("测试前提不成立：映射应已写入")
	}

	// resolve 会 stat 失败 → 忘记映射 → 尝试下载（404，必然失败）。
	_, _ = c.resolve(context.Background(), url, 1<<20, true)

	if _, ok := c.memLookup(urlKey); ok {
		t.Error("文件已丢失时映射应被清除，否则每次都会返回死链路径")
	}
}

// TestDedupeResultsByContent 锁住一条 BLOCKER 级回归。
//
// 一批 URL 里多个不同 URL 完全可能解析到同一内容（CDN 签名参数、
// http/https 双写、转载站）。register 会把这些结果作为一条多行
// INSERT ... ON CONFLICT DO UPDATE 的输入，而 PostgreSQL 不允许
// 同一语句内两行命中同一个冲突键：
//
//	21000: ON CONFLICT DO UPDATE command cannot affect row a second time
//
// 后果是整批登记失败，不是丢一行。
func TestDedupeResultsByContent(t *testing.T) {
	const hash = "51dfc9141a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5"

	results := []*imageResult{
		{sourceURL: "https://a/x.jpg", cacheKey: hash, filename: "f1.png"},
		{sourceURL: "https://a/x.jpg?utm=1", cacheKey: hash, filename: "f2.png"},
		{sourceURL: "https://b/y.jpg", cacheKey: "otherhash", filename: "f3.png"},
	}

	got := dedupeResultsByContent(results)

	if len(got) != 2 {
		t.Fatalf("去重后 %d 条，期望 2（重复 cache_key 会让 upsert 报 21000）", len(got))
	}

	// 断言没有重复的 cacheKey —— 这才是决定性条件。
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r.cacheKey] {
			t.Fatalf("cacheKey %q 重复出现", r.cacheKey)
		}
		seen[r.cacheKey] = true
	}
}

// TestDedupeResultsPreservesFilename 确认保留的是有文件名的那个结果，
// 避免留下一条指向不存在文件的记录。
func TestDedupeResultsPreservesFilename(t *testing.T) {
	const hash = "aabbccdd"

	results := []*imageResult{
		{sourceURL: "https://a/x.jpg", cacheKey: hash, filename: ""},
		{sourceURL: "https://a/x.jpg?x=1", cacheKey: hash, filename: "real.png"},
	}

	got := dedupeResultsByContent(results)
	if len(got) != 1 {
		t.Fatalf("应只剩 1 条，实际 %d", len(got))
	}
	if got[0].filename != "real.png" {
		t.Errorf("应保留有文件名的结果，实际 %q", got[0].filename)
	}
}

// TestDedupeResultsDropsEmptyCacheKey 确认没有内容键的结果不会进入
// 批量写入（空串会占住唯一索引，也可能撞键）。
func TestDedupeResultsDropsEmptyCacheKey(t *testing.T) {
	results := []*imageResult{
		{sourceURL: "https://a/x.jpg", cacheKey: "", filename: "f.png"},
		{sourceURL: "https://b/y.jpg", cacheKey: "h1", filename: "g.png"},
	}

	got := dedupeResultsByContent(results)
	if len(got) != 1 || got[0].cacheKey != "h1" {
		t.Fatalf("应只保留有内容键的那条，实际 %+v", got)
	}
}

// TestCacheSkipsURLWhenRegisterFails 锁住「永久坏图」回归。
//
// Cache 会把站内 URL 写回正文，而正文一旦写入就不会重算
// （fillEntryContent 只处理空正文）。所以登记失败的 URL 绝不能
// 写进去——文件不在库里，Sweep 迟早会把它当孤儿删掉，图片永久 404。
func TestCacheSkipsURLWhenRegisterFails(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)

	dir := t.TempDir()
	payload := []byte("\x89PNG\r\n\x1a\nhello")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	contentHash := sha256BytesHex(payload)

	// 必须按真实 SQL 序列写期望，否则测试会在"意外路径"上通过。
	//
	// 顺序：resolve 先查 URL 映射 → 未命中 → download →
	// lookupByContent 按内容查 → 未命中 → register 前置查询 →
	// register 写入（此处失败）。
	mock.ExpectQuery(`SELECT \* FROM "rss_image_sources" WHERE url_key = \$1.*LIMIT`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "image_id", "url_key", "source_url",
		}))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key = \$1.*LIMIT`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))
	// 关键：登记写入失败（模拟 DB 抖动）。
	// 写入在事务里，失败后必须回滚——回滚保证图片行/来源/引用
	// 一行都不留下，不会出现“部分提交”。
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WillReturnError(errors.New("模拟数据库抖动"))
	mock.ExpectRollback()

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}
	src := srv.URL + "/a.png"

	out, err := c.Cache(context.Background(), 1, []string{src})

	if err == nil {
		t.Fatal("登记失败时必须返回 error，否则调用方会把没有图的正文写进库")
	}
	if len(out) != 0 {
		t.Fatalf("登记失败时不应返回站内 URL，实际 %v（会变成永久坏图）", out)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足（可能走了非预期路径）: %v", err)
	}
	_ = contentHash
}

// TestMemCacheIsBounded 锁住一条长期运行回归。
//
// 时效签名 URL 每轮抓取都产生新的 urlKey。没有上限时进程内映射会
// 随时间单调增长——HomeHub 是长期跑在 NAS 上的常驻进程，
// 这种泄漏不会自己恢复。
func TestMemCacheIsBounded(t *testing.T) {
	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}

	// 写入超过上限的条目。
	for i := 0; i < memURLLimit+100; i++ {
		c.memRemember(sha256Hex(string(rune(i))+"-url"), memEntry{
			filename: "f.png",
			cacheKey: "k",
		})
	}

	c.memMu.RLock()
	size := len(c.memURL)
	c.memMu.RUnlock()

	if size > memURLLimit {
		t.Fatalf("进程内映射大小 %d 超过上限 %d，会无限增长", size, memURLLimit)
	}
	if size == 0 {
		t.Fatal("映射不应被清成空（未继续写入时应保留最近条目）")
	}
}

// TestPendingIsRefCounted 锁住并发批次互相摘掉保护的回归。
//
// 两个并发抓取批次可能解析到同一个内容键（不同源转载同一篇带图文章、
// 或时效签名 URL 指向同一张图）。若 pending 是集合而非引用计数：
// 先完成的批次 release 时 delete 掉该键，后完成的那批就失去保护——
// 此时它的文件还没登记引用，Sweep 会把文件删掉，随后 register 拿着
// 已删除的文件名写库，形成死链。
func TestPendingIsRefCounted(t *testing.T) {
	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}

	const shared = "shared-content-key"

	batchA := c.markPending(nil)
	batchB := c.markPending(nil)

	batchA.add(shared)
	batchB.add(shared)

	if !c.isPending(shared) {
		t.Fatal("两个批次都标记后应处于 pending")
	}

	// A 先完成：B 仍在处理，键必须继续可见。
	batchA.release()
	if !c.isPending(shared) {
		t.Fatal("A 释放后 B 仍在处理，键不应从 pending 消失（会导致 Sweep 误删）")
	}

	// B 也完成：此时才应清除。
	batchB.release()
	if c.isPending(shared) {
		t.Fatal("所有批次释放后键应被清除，否则该图永远不会被 Sweep 回收")
	}
}

// TestPendingAddIsIdempotentPerTracker 确认同一 tracker 重复 add 同一键
// 不会让引用计数虚高（否则 release 一次减不回去，键永远挂着）。
func TestPendingAddIsIdempotentPerTracker(t *testing.T) {
	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}

	const key = "dup-key"
	tracker := c.markPending(nil)

	tracker.add(key)
	tracker.add(key)
	tracker.add(key)

	tracker.release()

	if c.isPending(key) {
		t.Fatal("同一 tracker 重复 add 后 release 一次应完全清除")
	}
}

// TestIsPendingIgnoresZeroCount 确认计数为 0 的键不算 pending。
func TestIsPendingIgnoresZeroCount(t *testing.T) {
	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix, pending: map[string]int{"zero": 0}}
	if c.isPending("zero") {
		t.Error("计数为 0 不应视为 pending")
	}
	if c.isPending("missing") {
		t.Error("不存在的键不应视为 pending")
	}
}

// TestCacheConcurrentSameInstance 确认同一实例被多 goroutine 并发调用时
// 不产生数据竞争（配合 -race 运行）。
//
// 无数据库模式：走进程内 memURL 映射，这条路径上的读写全部要加锁。
// 真实场景是多个订阅源并发抓取共享同一个 LocalImageCache 实例。
func TestCacheConcurrentSameInstance(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n" + r.URL.Path))
	}))
	defer srv.Close()

	c := &LocalImageCache{dir: t.TempDir(), publicPrefix: imageURLPrefix}

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 一半 goroutine 请求同一 URL（争用同一映射键），
			// 另一半请求不同 URL。
			src := srv.URL + "/shared.png"
			if i%2 == 0 {
				src = srv.URL + "/unique-" + string(rune('a'+i)) + ".png"
			}
			out, err := c.Cache(context.Background(), uint64(i+1), []string{src})
			if err != nil {
				t.Errorf("goroutine %d: Cache 失败: %v", i, err)
				return
			}
			if len(out) != 1 {
				t.Errorf("goroutine %d: 期望 1 个映射，实际 %v", i, out)
			}
		}(i)
	}
	wg.Wait()

	// 内存映射不应超过上限。
	c.memMu.RLock()
	size := len(c.memURL)
	c.memMu.RUnlock()
	if size > memURLLimit {
		t.Errorf("memURL 大小 %d 超过上限 %d", size, memURLLimit)
	}
}

// TestCacheConcurrentDeduplicatesContent 确认并发下载同一内容时，
// 最终数据库层面只会登记一份（去重逻辑生效），不会因多行同键而失败。
//
// 无 db 模式下无法验证 DB 写入，这里验证派生的文件命名与内存映射
// 不会互相覆盖。
func TestCacheConcurrentDeduplicatesContent(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_allow_private_hosts": true, "image_max_bytes": 1048576}`)

	// 所有 URL 返回完全相同的字节 → 同一内容哈希。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nidentical-content"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	c := &LocalImageCache{dir: dir, publicPrefix: imageURLPrefix}

	const workers = 6
	var wg sync.WaitGroup
	results := make([]map[string]string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := c.Cache(context.Background(), 1, []string{srv.URL + "/img.png?n=" + string(rune('0'+i))})
			if err != nil {
				t.Errorf("goroutine %d: Cache 失败: %v", i, err)
				return
			}
			results[i] = out
		}(i)
	}
	wg.Wait()

	// 所有 goroutine 应指向同一个文件（内容相同）。
	var first string
	for i, r := range results {
		if len(r) != 1 {
			t.Fatalf("goroutine %d 期望 1 个映射，实际 %v", i, r)
		}
		for _, u := range r {
			if first == "" {
				first = u
			} else if u != first {
				t.Errorf("同内容返回了不同 URL: %q vs %q", first, u)
			}
		}
	}
}

// TestAdoptSameContentReturnsRegisteredFilename 锁住一条 MAJOR 回归。
//
// Adopt 的入参来自 extension，完全可能有两个 item 内容相同（同一张图被
// 两条 URL 引用，extension 各自落了一份文件）。
//
// register 内部按内容去重，只登记其中一条；若 Adopt 按**未去重的原始
// results** 输出，被丢弃那条会用自己的 item.Filename —— 那个文件在
// rss_images 里没有任何记录，Sweep 第三阶段会把它当历史遗留删掉，
// 而正文只写一次，链接永久 404。
//
// 断言：两个 URL 都必须指向**已登记的那个**文件名，而不是各自的入参名。
func TestAdoptSameContentReturnsRegisteredFilename(t *testing.T) {
	dir := t.TempDir()

	const payload = "identical-bytes"
	// extension 各自落了一份文件，字不同、内容相同。
	const fileA = "rss_ext_aaaa_11111111.png"
	const fileB = "rss_ext_bbbb_11111111.png"
	for _, n := range []string{fileA, fileB} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	contentHash := sha256BytesHex([]byte(payload))

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	// register 前置查询：无历史记录。
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))

	mock.ExpectBegin()
	// 去重后只写一行（fileA 或 fileB，取决于稳定排序的第一条）。
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "https://a/1.png", fileA,
			"image/png", len(payload), nil,
		))
	mock.ExpectQuery(`INSERT INTO "rss_image_sources".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`INSERT INTO "rss_image_refs"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}

	out := c.Adopt(9, []AdoptedImage{
		{SourceURL: "https://a/1.png", Filename: fileA},
		{SourceURL: "https://a/2.png", Filename: fileB},
	})

	if len(out) != 2 {
		t.Fatalf("两个 URL 都应有映射，实际 %v", out)
	}

	// 两条 URL 必须指向同一个（已登记的）文件名。
	want := c.publicPrefix + fileA
	for src, got := range out {
		if got != want {
			t.Errorf("URL %s 指向 %q，期望已登记的 %q（否则该文件会被 Sweep 删除）", src, got, want)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// TestAdoptRejectsMismatchedHash 确认 extension 传入错误哈希时以文件为准，
// 不会让错误键污染唯一索引（可能把另一张图的记录改指向本次文件）。
func TestAdoptRejectsMismatchedHash(t *testing.T) {
	dir := t.TempDir()
	const name = "rss_ext_cccc_22222222.png"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("real-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	realHash := sha256BytesHex([]byte("real-bytes"))

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))
	mock.ExpectBegin()
	// 断言写入使用的是真实内容哈希，而不是传入的假值。
	// 注意 SQL 已参数化，哈希在参数里不在文本中，因此用 WithArgs 匹配。
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			realHash, "https://a/1.png", name, "image/png", 10, sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, realHash, "https://a/1.png", name,
			"image/png", 10, nil,
		))
	mock.ExpectQuery(`INSERT INTO "rss_image_sources".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}

	fakeHash := strings.Repeat("de", 32)
	out := c.Adopt(0, []AdoptedImage{
		{SourceURL: "https://a/1.png", Filename: name, SHA256: fakeHash},
	})

	if len(out) != 1 {
		t.Fatalf("应登记 1 条，实际 %v", out)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足（可能用了传入的假哈希）: %v", err)
	}
}

// TestRegisterRollsBackWhenRefFails 锁住一条静默失效回归。
//
// 图片行写入成功、但条目引用写入失败时，如果不在同一事务里，文件会
// 存在却没有引用保护：Sweep 的回收条件是「last_seen_at 过期 且 无引用」，
// 30 天后就把文件删了。而正文只写一次（fillEntryContent 只处理空正文），
// 那个站内 URL 就永久 404。
//
// 正确行为：整个事务回滚 → register 返回 nil → 调用方不写 URL →
// 条目正文保持为空 → 下次抓取重试。
func TestRegisterRollsBackWhenRefFails(t *testing.T) {
	dir := t.TempDir()
	const name = "rss_ts_rand_aaaaaaaa.png"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	contentHash := sha256BytesHex([]byte("img"))

	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "rss_images".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT \* FROM "rss_images" WHERE cache_key IN`).
		WillReturnRows(sqlmock.NewRows(rssImageColumns()).AddRow(
			1, nil, nil, nil, contentHash, "https://a/1.png", name,
			"image/png", 3, nil,
		))
	mock.ExpectQuery(`INSERT INTO "rss_image_sources".*ON CONFLICT`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	// 关键：引用写入失败。
	mock.ExpectQuery(`INSERT INTO "rss_image_refs"`).
		WillReturnError(errors.New("模拟引用写入失败"))
	mock.ExpectRollback()

	c := &LocalImageCache{db: db, dir: dir, publicPrefix: imageURLPrefix}

	registered := c.register(7, []*imageResult{{
		sourceURL:   "https://a/1.png",
		cacheKey:    contentHash,
		filename:    name,
		urlKey:      sha256Hex("https://a/1.png"),
		contentType: "image/png",
		sizeBytes:   3,
		sha256:      contentHash,
	}})

	if len(registered) != 0 {
		t.Errorf("引用写入失败时必须整体回滚并返回空（否则文件无引用保护，30 天后被删）: %v", registered)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("SQL 期望未满足: %v", err)
	}
}

// failingImageCache 是恒返回登记失败的替身。
type failingImageCache struct{}

func (failingImageCache) Cache(context.Context, uint64, []string) (map[string]string, error) {
	return nil, errors.New("模拟登记失败")
}

func (failingImageCache) Sweep(context.Context, time.Duration) error { return nil }

// TestSanitizeContentKeepsBodyEmptyWhenRegisterFails 锁住「永久丢图」回归。
//
// 正文一旦落库就不会重算（fillEntryContent 只处理空正文）。图片登记失败
// 时若仍返回“没有图的正文”，这批图片就永久丢失且无自愈路径。
//
// 正确行为：返回空串 → 调用方不写正文 → 下次抓取重新处理。
func TestSanitizeContentKeepsBodyEmptyWhenRegisterFails(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": true}`)
	withTempCacheDir(t)

	s := &RSSService{images: failingImageCache{}}
	content := `<p>正文</p><img src="http://example.com/a.png">`

	got := s.sanitizeContent(context.Background(), 1, content)

	if got != "" {
		t.Fatalf("登记失败时应返回空正文以便下次重试，实际 %q（图片会永久丢失）", got)
	}
}

// TestSanitizeContentRendersWhenImagesJustFail 确认“图片下载不到”是常态，
// 不该阻止正文写入。
//
// 与上一条的区别：这里 Cache 正常返回（只是映射为空，因为图 404），
// 说明登记本身成功了，正文应当落库（图片标签被移除）。
func TestSanitizeContentRendersWhenImagesJustFail(t *testing.T) {
	loadConfigWith(t, `,"rss": {"image_cache_enabled": true}`)
	withTempCacheDir(t)

	s := &RSSService{images: &fakeImageCache{}} // 返回空映射、无 error
	content := `<p>正文</p><img src="http://example.com/a.png">`

	got := s.sanitizeContent(context.Background(), 1, content)

	if got == "" {
		t.Fatal("图片只是下载不到时不应阻止正文写入")
	}
	if !strings.Contains(got, "正文") {
		t.Errorf("正文内容丢失: %q", got)
	}
	if strings.Contains(got, "a.png") {
		t.Errorf("不可用的图片标签应被移除: %q", got)
	}
}
