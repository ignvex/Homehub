-- HomeHub RSS 条目跨源共享改造
--
-- ⚠️ 历史存档。本脚本里的表名带 `homehub_` 前缀，对应当年的 schema。
-- 现在表名已去掉前缀（见 docs/tables.md 第 1 节），**全新部署不需要执行
-- 本脚本**——AutoMigrate 会直接建出最终结构。
--
-- 只有一种情况仍需要它：从一个保持旧表名、且数据不能丢的老库升级。
-- 那种情况下先把表名改成无前缀（ALTER TABLE ... RENAME），再按下面执行。
--
-- 背景：rss_entries.link 之前是全表唯一，两个源收录同一链接时
-- 后抓的那个源看不到条目。改为"条目 = 纯内容，归属放关联表"后，一条
-- 内容可以被任意多个源收录，删除其中一个源不会影响其他源。
--
-- 执行方式（必须手工，启动流程会拒绝在未迁移的库上运行）：
--   psql -h <host> -U <user> -d <dbname> -f docs/migrations/001-rss-shared-entries.sql
--
-- 要求：PostgreSQL 11+（用到内置 sha256()）。幂等，可重复执行。先备份。

BEGIN;

-- ============================================================
-- 1. 条目归属关联表
-- ============================================================
CREATE TABLE IF NOT EXISTS homehub_rss_entry_feeds (
    id            BIGSERIAL PRIMARY KEY,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ,
    entry_id      BIGINT NOT NULL,
    feed_id       BIGINT NOT NULL,
    published     TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_rss_entry_feed
    ON homehub_rss_entry_feeds (entry_id, feed_id);
CREATE INDEX IF NOT EXISTS idx_homehub_rss_entry_feeds_feed_id
    ON homehub_rss_entry_feeds (feed_id);
CREATE INDEX IF NOT EXISTS idx_homehub_rss_entry_feeds_published
    ON homehub_rss_entry_feeds (published);
CREATE INDEX IF NOT EXISTS idx_homehub_rss_entry_feeds_deleted_at
    ON homehub_rss_entry_feeds (deleted_at);

-- 把现有的"条目 → 源"搬进关联表。
-- 迁移前每个条目只属于一个源（当年被 ON CONFLICT 吞掉的重复链接不可恢复）。
INSERT INTO homehub_rss_entry_feeds
    (entry_id, feed_id, published, first_seen_at, last_seen_at, created_at, updated_at)
SELECT id, feed_id, published, fetched_at, fetched_at, created_at, updated_at
FROM homehub_rss_entries
WHERE deleted_at IS NULL
  AND feed_id IS NOT NULL
ON CONFLICT (entry_id, feed_id) DO NOTHING;

-- ============================================================
-- 2. 图片引用改为指向条目
-- ============================================================
ALTER TABLE homehub_rss_image_refs
    ADD COLUMN IF NOT EXISTS entry_id BIGINT;

-- 按原 feed_id + entry_hash 找回条目。entry_hash = sha256(link)。
UPDATE homehub_rss_image_refs r
SET entry_id = e.id
FROM homehub_rss_entries e
WHERE r.entry_id IS NULL
  AND e.deleted_at IS NULL
  AND e.feed_id = r.feed_id
  AND e.link IS NOT NULL
  AND encode(sha256(convert_to(e.link, 'UTF8')), 'hex') = r.entry_hash;

-- 无法匹配到条目的引用是脏数据（条目已不存在），直接删。
-- 万一 sha256() 不可用，这里的整个 UPDATE 会报错；此时改用：
--   DELETE FROM homehub_rss_image_refs;
-- 应用启动后的 BackfillLegacy 会按条目正文重建引用，图片文件不受影响。
DELETE FROM homehub_rss_image_refs WHERE entry_id IS NULL;

ALTER TABLE homehub_rss_image_refs
    ALTER COLUMN entry_id SET NOT NULL;

-- 换掉旧的三列联合唯一索引，改为 (image_id, entry_id)。
DROP INDEX IF EXISTS idx_rss_image_ref;
CREATE UNIQUE INDEX IF NOT EXISTS idx_rss_image_ref
    ON homehub_rss_image_refs (image_id, entry_id);
CREATE INDEX IF NOT EXISTS idx_homehub_rss_image_refs_entry_id
    ON homehub_rss_image_refs (entry_id);

-- 旧列不再写入，放开 NOT NULL 让新插入通过（不删列，便于回滚）。
ALTER TABLE homehub_rss_image_refs
    ALTER COLUMN feed_id DROP NOT NULL,
    ALTER COLUMN entry_hash DROP NOT NULL;

-- ============================================================
-- 3. 条目表旧列放开约束
-- ============================================================
ALTER TABLE homehub_rss_entries
    ALTER COLUMN feed_id DROP NOT NULL;

-- ============================================================
-- 4. 核对
-- ============================================================
-- 条目数与关联表应一致（迁移前每条只有一个源）：
--   SELECT count(*) FROM homehub_rss_entries WHERE deleted_at IS NULL;
--   SELECT count(*) FROM homehub_rss_entry_feeds WHERE deleted_at IS NULL;
-- 图片引用是否都有归属：
--   SELECT count(*) FROM homehub_rss_image_refs WHERE entry_id IS NULL;

COMMIT;

-- ============================================================
-- 回滚（仅在退回旧版本时执行）
-- ============================================================
-- BEGIN;
-- UPDATE homehub_rss_entries e
-- SET feed_id = ef.feed_id
-- FROM (
--     SELECT DISTINCT ON (entry_id) entry_id, feed_id
--     FROM homehub_rss_entry_feeds
--     WHERE deleted_at IS NULL
--     ORDER BY entry_id, id ASC
-- ) ef
-- WHERE e.id = ef.entry_id AND e.feed_id IS NULL;
--
-- ALTER TABLE homehub_rss_entries ALTER COLUMN feed_id SET NOT NULL;
-- DELETE FROM homehub_rss_image_refs;  -- 旧版会重新回填
-- COMMIT;
