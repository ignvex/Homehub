-- 006: 数据库表统一加模块前缀
-- homehub_ 前缀：核心模块
-- bilibili_ 前缀：B站爬虫模块

ALTER TABLE IF EXISTS users                    RENAME TO homehub_users;
ALTER TABLE IF EXISTS sessions                RENAME TO homehub_sessions;
ALTER TABLE IF EXISTS bookmarks               RENAME TO homehub_bookmarks;
ALTER TABLE IF EXISTS bookmark_groups         RENAME TO homehub_bookmark_groups;
ALTER TABLE IF EXISTS log_entries             RENAME TO homehub_log_entries;
ALTER TABLE IF EXISTS media_assets            RENAME TO homehub_media_assets;
ALTER TABLE IF EXISTS pastes                  RENAME TO homehub_pastes;
ALTER TABLE IF EXISTS avatar_caches           RENAME TO homehub_avatar_caches;
ALTER TABLE IF EXISTS rss_feeds               RENAME TO homehub_rss_feeds;
ALTER TABLE IF EXISTS rss_entries             RENAME TO homehub_rss_entries;
ALTER TABLE IF EXISTS rss_groups              RENAME TO homehub_rss_groups;
ALTER TABLE IF EXISTS todos                   RENAME TO homehub_todos;
ALTER TABLE IF EXISTS crawler_state           RENAME TO homehub_crawler_state;

ALTER TABLE IF EXISTS bili_users              RENAME TO bilibili_users;
ALTER TABLE IF EXISTS videos                  RENAME TO bilibili_videos;
ALTER TABLE IF EXISTS video_pages             RENAME TO bilibili_video_pages;
ALTER TABLE IF EXISTS articles                RENAME TO bilibili_articles;
ALTER TABLE IF EXISTS video_stats_snapshots   RENAME TO bilibili_video_stats_snapshots;
ALTER TABLE IF EXISTS article_stats_snapshots RENAME TO bilibili_article_stats_snapshots;
ALTER TABLE IF EXISTS user_stats_snapshots    RENAME TO bilibili_user_stats_snapshots;
