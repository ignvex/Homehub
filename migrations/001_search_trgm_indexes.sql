-- HomeHub 搜索性能优化：pg_trgm GIN 索引
-- 
-- 背景：search_service.go 使用 ILIKE '%keyword%' 进行模糊搜索，
-- 前后通配符导致 B-tree 索引完全失效。pg_trgm 扩展提供三元组
-- (trigram) 索引，支持 LIKE/ILIKE 前后模糊匹配的索引加速。
--
-- 执行方式：
--   psql -U homehub -d homehub -f migrations/001_search_trgm_indexes.sql
--
-- 前置条件：PostgreSQL 已安装 pg_trgm 扩展（通常内置）

-- 1. 启用 pg_trgm 扩展
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 2. 书签表 — title + url
CREATE INDEX IF NOT EXISTS idx_bookmarks_title_trgm
    ON bookmarks USING GIN (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_bookmarks_url_trgm
    ON bookmarks USING GIN (url gin_trgm_ops);

-- 3. 视频表 — title + desc
CREATE INDEX IF NOT EXISTS idx_videos_title_trgm
    ON videos USING GIN (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_videos_desc_trgm
    ON videos USING GIN ("desc" gin_trgm_ops);

-- 4. 专栏表 — title
CREATE INDEX IF NOT EXISTS idx_articles_title_trgm
    ON articles USING GIN (title gin_trgm_ops);

-- 5. B 站用户表 — name
CREATE INDEX IF NOT EXISTS idx_bili_users_name_trgm
    ON bili_users USING GIN (name gin_trgm_ops);

-- 验证索引创建
-- SELECT indexname, tablename FROM pg_indexes WHERE indexname LIKE '%_trgm';
