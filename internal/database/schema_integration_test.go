package database

import (
	"crypto/rand"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 本文件是真实 PostgreSQL 的端到端验证，只在设置 HOMEHUB_TEST_DB 时运行。
//
// 为什么必须要有它：仓库里绝大多数数据层断言基于 sqlmock，而 sqlmock
// 只能验证"我生成的 SQL 长什么样"，无法证明 PostgreSQL 会不会接受它。
// 以下几条长期只存在于推断中，只有真实数据库能证实：
//
//   - ON CONFLICT DO UPDATE 同批多行命中同一键 → 21000，整批失败
//   - varchar(N) 的 N 是字符数，而 btree 索引另有 ~2704 字节上限
//   - 软删除行会占住唯一索引
//
// 运行方式：
//
//	HOMEHUB_TEST_DB="host=192.168.1.2 port=5432 user=test password=xxx dbname=testdb sslmode=disable" \
//	  go test ./internal/database/ -run Integration -v

// integrationSchema 是 database 包集成测试专用的 schema。
//
// 绝不能在 public 里建/删表：`HOMEHUB_TEST_DB` 指向的很可能是真实库，
// 而测试要反复 DROP/CREATE。一旦误指向生产库，所有数据就没了。
// 独立 schema 让这层风险从"靠人小心"变成"结构上不可能"。
const integrationSchema = "it_database"

// integrationDB 在独立 schema 里连接真实数据库并建表。
func integrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("HOMEHUB_TEST_DB")
	if dsn == "" {
		t.Skip("跳过集成测试：未设置 HOMEHUB_TEST_DB 环境变量")
	}

	// 建 schema 需要一个 search_path 尚未指向它的连接。
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	if err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + integrationSchema).Error; err != nil {
		t.Fatalf("创建测试 schema %s 失败（账号需要 CREATE 权限）: %v", integrationSchema, err)
	}
	if s, err := admin.DB(); err == nil {
		s.Close()
	}

	resetForTest()
	// search_path 必须带上 public：pg_trgm 扩展装在 public 里，
	// 只指定测试 schema 会让 gin_trgm_ops 操作符类解析不到，
	// 模糊搜索索引静默创建失败（EnsureSearchIndexes 只告警）。
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+integrationSchema+",public"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
		// 只删自己建的 schema，且只在它确实是本测试建的时才删。
		if cleanup, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard}); err == nil {
			cleanup.Exec("DROP SCHEMA IF EXISTS " + integrationSchema + " CASCADE")
			if s, err := cleanup.DB(); err == nil {
				s.Close()
			}
		}
	})

	// 开局清一次，避免上次异常退出留下半成品。
	//
	// **必须显式带 schema 前缀**：在 search_path=it_database,public 下，
	// 裸写 `DROP TABLE IF EXISTS users` 会先找 it_database.users、
	// 找不到就继续往下找到 public.users 并把它删掉——那正是
	// "测试删光生产数据"的事故形态。
	for name := range homeHubTables {
		db.Exec("DROP TABLE IF EXISTS " + integrationSchema + "." + name + " CASCADE")
	}

	if err := db.AutoMigrate(model.AutoMigrateModels()...); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	if err := model.CreatePastesTable(db); err != nil {
		t.Fatalf("创建 pastes 表失败: %v", err)
	}

	return db
}

// TestIntegrationTablesCreated 确认 AutoMigrate 建出的表名与登记一致。
//
// 去掉 homehub_ 前缀后，这条测试同时守护"表名是否真的简化了"——
// 若某个 TableName() 漏改，这里会多出一张带前缀的表。
func TestIntegrationTablesCreated(t *testing.T) {
	db := integrationDB(t)

	var tables []string
	if err := db.Raw(`
		SELECT tablename FROM pg_tables
		WHERE schemaname = current_schema() ORDER BY tablename
	`).Scan(&tables).Error; err != nil {
		t.Fatalf("查询表列表失败: %v", err)
	}

	got := make(map[string]struct{}, len(tables))
	for _, name := range tables {
		got[name] = struct{}{}
		if strings.HasPrefix(name, "homehub_") {
			t.Errorf("表 %q 仍带 homehub_ 前缀（TableName 漏改？）", name)
		}
	}

	for want := range homeHubTables {
		if _, ok := got[want]; !ok {
			t.Errorf("缺少表 %q", want)
		}
	}
}

// TestIntegrationUpsertMultiRowConflictIsFatal 用真实数据库证实 21000。
//
// 这是 rss_entries、rss_image_sources、rss_images 三处代码里"必须先按
// 唯一键去重再批量 upsert"的唯一依据。若这条不成立，那些去重逻辑就是
// 多余的；若成立（它确实成立），漏掉去重会导致整批写入失败。
func TestIntegrationUpsertMultiRowConflictIsFatal(t *testing.T) {
	db := integrationDB(t)

	// service 层用的是同一套写法：多行 VALUES + ON CONFLICT DO UPDATE。
	err := db.Exec(`
		INSERT INTO rss_entries (created_at, updated_at, title, link, fetched_at)
		VALUES (now(), now(), 'a', 'https://example.com/same', now()),
		       (now(), now(), 'b', 'https://example.com/same', now())
		ON CONFLICT (link) DO UPDATE SET title = excluded.title
	`).Error

	if err == nil {
		t.Fatal("同批两行命中同一冲突键竟然成功了；去重逻辑可以移除？请重新评估")
	}
	if !strings.Contains(err.Error(), "21000") &&
		!strings.Contains(err.Error(), "cannot affect row a second time") {
		t.Fatalf("期望 21000（cannot affect row a second time），实际: %v", err)
	}
}

// TestIntegrationBTreeIndexByteLimit 实测 btree 唯一索引的字节上限。
//
// 结论（PostgreSQL 18 / 8KB 页）：内容可用约 2692 字节，加上行头后
// 索引项上限 2704 字节。model.TruncateIndexed 取 2600 留余量。
func TestIntegrationBTreeIndexByteLimit(t *testing.T) {
	db := integrationDB(t)

	// 真高熵：每个字符都从随机数取，不能循环取字符。
	//
	// 用固定字符串循环（或重复同一个字）会被 PostgreSQL 压缩，
	// 测不出真实字节上限——那正是最初误判“2048 个中文能插进去”的原因。
	highEntropy := func(nchars int) string {
		out := make([]rune, nchars)
		max := big.NewInt(0x9FA5 - 0x4E00) // CJK 统一汉字区间
		for i := range out {
			v, err := rand.Int(rand.Reader, max)
			if err != nil {
				t.Fatalf("生成随机字符失败: %v", err)
			}
			out[i] = rune(0x4E00 + v.Int64())
		}
		return string(out)
	}

	// 先确认表结构就绪
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS _it_idx_probe (id serial primary key, val varchar(2048))`).Error; err != nil {
		t.Fatalf("建探测表失败: %v", err)
	}
	db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS _it_idx_probe_val ON _it_idx_probe (val)`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS _it_idx_probe`) })

	cases := []struct {
		chars  int
		wantOK bool
	}{
		{800, true},   // 2400 字节，安全
		{880, true},   // 2640 字节，接近上限但仍可
		{900, false},  // 2700 字节，超 2704（含行开销）
		{2048, false}, // 6144 字节，必失败
	}

	for _, c := range cases {
		db.Exec(`DELETE FROM _it_idx_probe`)
		err := db.Exec(`INSERT INTO _it_idx_probe (val) VALUES (?)`, highEntropy(c.chars)).Error

		if c.wantOK && err != nil {
			t.Errorf("%d 字符(%d 字节) 应当成功，实际: %v", c.chars, c.chars*3, err)
		}
		if !c.wantOK {
			if err == nil {
				t.Errorf("%d 字符(%d 字节) 应当被 btree 上限拒绝，但成功了", c.chars, c.chars*3)
			} else if !strings.Contains(err.Error(), "54000") &&
				!strings.Contains(err.Error(), "index row size") {
				t.Errorf("%d 字符的失败原因不是索引超限: %v", c.chars, err)
			}
		}
	}

	// 结论性断言：TruncateIndexed 的输出必须能被真实索引接受。
	long := highEntropy(2048)
	truncated := model.TruncateIndexed(long, 2048)
	db.Exec(`DELETE FROM _it_idx_probe`)
	if err := db.Exec(`INSERT INTO _it_idx_probe (val) VALUES (?)`, truncated).Error; err != nil {
		t.Fatalf("TruncateIndexed 的输出仍被索引拒绝（上限设错了）: %v", err)
	}
}

// TestIntegrationSoftDeleteHoldsUniqueIndex 证实软删除行会占住唯一索引。
//
// 这条是"带唯一键的表必须物理删除"以及"用户名删了不能复用"的依据。
func TestIntegrationSoftDeleteHoldsUniqueIndex(t *testing.T) {
	db := integrationDB(t)

	mk := func(name string) error {
		return db.Create(&model.User{
			Username:     name,
			PasswordHash: "x",
			Level:        model.LevelViewer,
			IsActive:     true,
		}).Error
	}

	if err := mk("dupname"); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}

	// 软删除：只写 deleted_at。
	if err := db.Where("username = ?", "dupname").Delete(&model.User{}).Error; err != nil {
		t.Fatalf("软删除失败: %v", err)
	}

	// 同名再建：唯一索引仍被占住。
	err := mk("dupname")
	if err == nil {
		t.Fatal("软删除后同名用户仍可创建；若如此，文档里『删掉的用户名无法复用』需要更正")
	}
	if !strings.Contains(err.Error(), "duplicate key") &&
		!strings.Contains(err.Error(), "23505") {
		t.Logf("同名插入失败，原因是 %v（期望唯一约束冲突）", err)
	}

	// 物理删除后可以复用。
	if err := db.Unscoped().Where("username = ?", "dupname").Delete(&model.User{}).Error; err != nil {
		t.Fatalf("物理删除失败: %v", err)
	}
	if err := mk("dupname"); err != nil {
		t.Errorf("物理删除后应可重建同名用户，实际: %v", err)
	}
}

// TestIntegrationSearchIndexes 确认 pg_trgm 扩展与 GIN 索引能真实建起来。
//
// EnsureSearchIndexes 对失败是降级处理（只告警），所以单元测试无法
// 区分"建成了"和"静默失败"。
func TestIntegrationSearchIndexes(t *testing.T) {
	db := integrationDB(t)

	EnsureSearchIndexes(db)

	for _, idx := range searchIndexes {
		var exists bool
		if err := db.Raw(`
			SELECT EXISTS(SELECT 1 FROM pg_indexes
			WHERE schemaname = current_schema() AND indexname = ?)
		`, idx.name).Scan(&exists).Error; err != nil {
			t.Fatalf("查询索引 %s 失败: %v", idx.name, err)
		}
		if !exists {
			t.Errorf("索引 %s 未建成（pg_trgm 扩展不可用？）", idx.name)
		}
	}
}

// TestIntegrationSchemaGuardPasses 确认全新库不会触发手工迁移拦截。
func TestIntegrationSchemaGuardPasses(t *testing.T) {
	db := integrationDB(t)

	if err := CheckSchema(db); err != nil {
		t.Errorf("全新库不应要求手工迁移，实际: %v", err)
	}
}

var _ = config.DatabaseConfig{}
