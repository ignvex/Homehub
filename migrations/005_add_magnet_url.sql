-- 005: 为 rss_entries 新增 magnet_url 列，存储从 RSS 条目中提取的磁力链接
-- 注意：动漫花园等源的磁力链接含全部 tracker 参数，可能超过 2048 字符，故使用 TEXT 类型。
ALTER TABLE rss_entries ADD COLUMN IF NOT EXISTS magnet_url TEXT;
