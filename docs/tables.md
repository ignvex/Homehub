# 数据库表结构

HomeHub 的全部持久化表。用于人工复核与 schema 评审。

**权威性**：表结构由 `internal/app/app.go` 的 `AutoMigrate` 生成（`homehub_pastes`
例外，见下）。本文档中的列类型由 GORM 的 PostgreSQL dialector 解析得出，
与 AutoMigrate 实际执行一致；但**唯一事实源仍是 Go 模型**
（`internal/model/*.go`）。模型变更后请同步本文档。

- 数据库：PostgreSQL
- 表名前缀：`homehub_`（`internal/database/ddl_guard_test.go` 强制）
- 迁移方式：启动时 AutoMigrate（只增列/增索引，不做破坏性变更）

---

## 1. 共同约定

### 1.1 BaseModel

除 `homehub_pastes` 外，所有表都嵌入 `model.BaseModel`，固定含 4 列：

| 列 | 类型 | 说明 |
|----|------|------|
| `id` | `bigserial` | 自增主键 |
| `created_at` | `timestamptz` | 插入时间，GORM 自动维护 |
| `updated_at` | `timestamptz` | 更新时间，GORM 自动维护 |
| `deleted_at` | `timestamptz` | 软删除标记，NULL 表示未删除；带索引 |

`deleted_at` 由 `gorm.DeletedAt` 驱动：GORM 的 `Delete` 写时间戳，普通查询
自动加 `deleted_at IS NULL`，只有 `Unscoped()` 才物理删除。

### 1.2 软删除并非全表一致（重点）

拿到 `deleted_at` 不等于这张表就是软删除。当前实际行为：

| 表 | 删除语义 | 备注 |
|----|---------|------|
| `homehub_sessions` | 软（过期清理未用 `Unscoped`） | 过期行只打标记，不回收 |
| `homehub_rss_entries` | **物理** | 仅当条目成为孤儿（没有源收录）时删除 |
| `homehub_rss_image_refs` | **物理** | 随孤儿条目删除 |
| `homehub_rss_images` | **物理** | 回收孤儿时用 `Unscoped()`；否则 `cache_key` 唯一键冲突 |
| `homehub_rss_entry_feeds` | **物理** | 删源 / retention 都删关联行 |
| `homehub_rss_feeds` | **物理** | 删源用 `Unscoped()` |
| `homehub_pastes` | **物理** | 过期清理用 `Unscoped()` |
| `homehub_avatar_caches` | **物理** | 换头像时 `Unscoped()` 删旧行 |
| `homehub_bookmarks` / `homehub_bookmark_groups` | 软 | 分组删除时先软删书签 |
| `homehub_rss_groups` | 软 | 删分组只把 feed 的 `group_id` 置 NULL |
| `homehub_todos` | 软 | |
| `homehub_media_assets` | 软 | 物理文件另行删除 |
| `homehub_users` | 软 | |
| `homehub_log_entries` | 软 | 当前没有删除路径，只增不减 |

**评审要点**：软删除会占住唯一索引。任何带 `uniqueIndex` 的表如果要复用键值，
必须物理删除，否则重新插入会撞索引且被 GORM 的 `ON CONFLICT` 静默吞掉。

### 1.3 没有数据库外键

全库**不声明 FOREIGN KEY 约束**，关联只靠列值 + 应用层维护。
`RSSGroup.Feeds` / `BookmarkGroup.Bookmarks` 上的 `foreignKey:` 只是 GORM
的 `Preload` 提示，不生成约束。

排序规则使用数据库默认（通常 `en_US.UTF-8`），未显式指定 `COLLATE`。

---

## 2. 关系总图

```mermaid
erDiagram
    homehub_users ||--o{ homehub_sessions : "user_id"
    homehub_users ||--o| homehub_avatar_caches : "user_id 唯一"
    homehub_bookmark_groups ||--o{ homehub_bookmarks : "group_id"
    homehub_rss_groups ||--o{ homehub_rss_feeds : "group_id 可空"
    homehub_rss_feeds ||--o{ homehub_rss_entry_feeds : "feed_id"
    homehub_rss_entries ||--o{ homehub_rss_entry_feeds : "entry_id"
    homehub_rss_images ||--o{ homehub_rss_image_refs : "image_id"
    homehub_rss_entries ||--o{ homehub_rss_image_refs : "entry_id"

    homehub_users {
        bigserial id PK
        varchar username UK
    }
    homehub_sessions {
        bigserial id PK
        bigint user_id
        varchar token UK
    }
    homehub_avatar_caches {
        bigserial id PK
        bigint user_id UK
        bytea image_data
    }
    homehub_bookmark_groups {
        bigserial id PK
        varchar name
    }
    homehub_bookmarks {
        bigserial id PK
        bigint group_id
        varchar url
    }
    homehub_rss_groups {
        bigserial id PK
        varchar name
    }
    homehub_rss_feeds {
        bigserial id PK
        varchar url UK
        bigint group_id
    }
    homehub_rss_entries {
        bigserial id PK
        varchar link UK
        text content
    }
    homehub_rss_entry_feeds {
        bigserial id PK
        bigint entry_id
        bigint feed_id
        timestamptz published
    }
    homehub_rss_images {
        bigserial id PK
        varchar cache_key UK
        varchar filename
    }
    homehub_rss_image_refs {
        bigserial id PK
        bigint image_id
        bigint entry_id
    }
```

条目是**跨源共享的内容**：`homehub_rss_entries` 只存内容（按 `link` 全表
唯一），"哪个源收录了它"由 `homehub_rss_entry_feeds` 表达。一条内容可以
被任意多个源收录，删除其中一个源只删它那一行关联。

图片引用挂在**条目**上：内容被多源共享时图片只有一份引用，删掉任一源
都不会让图片变成孤儿。

图中未画的三张表与其他表没有关联：`homehub_todos`（全体共享）、
`homehub_log_entries`（只写日志）、`homehub_media_assets`（只被
`webui` 配置里的 asset id 引用，配置存在 JSON 文件而非数据库）、
`homehub_pastes`（完全独立）。

---

## 3. 认证与用户

### 3.1 `homehub_users`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| `created_at` / `updated_at` / `deleted_at` | `timestamptz` | `deleted_at` 有索引 | |
| `username` | `varchar(100)` | NOT NULL, **UNIQUE** | 登录名 |
| `password_hash` | `varchar(255)` | NOT NULL | bcrypt |
| `email` | `varchar(255)` | DEFAULT `''` | 用于 Gravatar |
| `role` | `varchar(20)` | NOT NULL, DEFAULT `'user'` | `admin` / `user` |
| `is_active` | `boolean` | DEFAULT `true` | 停用后不能登录 |

索引：`idx_homehub_users_username`（unique）、`idx_homehub_users_deleted_at`。

**评审要点**：`username` 唯一索引不含 `deleted_at` 条件，因此删掉的用户名
无法复用（软删除行仍占位）。`email` 没有唯一约束，允许多人同邮箱。

### 3.2 `homehub_sessions`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `user_id` | `bigint` | NOT NULL | 指向 `homehub_users.id`，无外键 |
| `token` | `varchar(255)` | NOT NULL, **UNIQUE** | 会话令牌 |
| `expires_at` | `timestamptz` | NOT NULL | 过期时间 |

索引：`idx_homehub_sessions_token`（unique）、`idx_homehub_sessions_user_id`、
`idx_homehub_sessions_deleted_at`。

过期记录由 `service.CleanupExpiredSessions` 每小时清理。

### 3.3 `homehub_avatar_caches`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `user_id` | `bigint` | NOT NULL, **UNIQUE** | 每个用户一份缓存 |
| `variant` | `varchar(160)` | NOT NULL | `来源\|尺寸\|风格\|分级` 指纹 |
| `email_hash` | `varchar(64)` | NOT NULL | 邮箱 MD5，变更后触发重取 |
| `image_data` | `bytea` | NOT NULL | 头像二进制 |
| `content_type` | `varchar(50)` | NOT NULL | 已校验的 MIME |
| `e_tag` | `varchar(255)` | | 上游 ETag（注意列名是 `e_tag`，GORM 拆分缩写） |
| `last_modified` | `varchar(100)` | | 上游 Last-Modified |

索引：`idx_homehub_avatar_caches_user_id`（unique）。

**评审要点**：头像二进制直接入库。1024px JPEG 约 100–300KB，用户量大时
`homehub_avatar_caches` 会成为最大的表；`user_id` 唯一索引保证每人一份。

---

## 4. 书签

### 4.1 `homehub_bookmark_groups`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `name` | `varchar(100)` | NOT NULL | 分组名 |
| `sort_order` | `bigint` | DEFAULT `0` | 上移/下移排序 |
| `priority` | `bigint` | DEFAULT `0` | 首页展示优先级 |

索引：仅 `idx_homehub_bookmark_groups_deleted_at`。

### 4.2 `homehub_bookmarks`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `group_id` | `bigint` | NOT NULL | 指向分组，无外键 |
| `title` | `varchar(255)` | NOT NULL | |
| `url` | `varchar(2048)` | NOT NULL | |
| `icon` | `varchar(2048)` | | 可为空串表示显式清空 |
| `sort_order` | `bigint` | DEFAULT `0` | |
| `priority` | `bigint` | DEFAULT `0` | |

索引：`idx_homehub_bookmarks_group_id`、`idx_homehub_bookmarks_deleted_at`，
外加 `database.EnsureSearchIndexes` 创建的两个 pg_trgm GIN 索引：
`idx_bookmarks_title_trgm(title)`、`idx_bookmarks_url_trgm(url)`。

**评审要点**：`url` 没有唯一约束，同一链接可重复收藏。

---

## 5. 媒体资源

### 5.1 `homehub_media_assets`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `filename` | `varchar(255)` | NOT NULL | 磁盘文件名（UUID） |
| `original_name` | `varchar(255)` | NOT NULL | 上传时的原名 |
| `file_path` | `varchar(512)` | NOT NULL | 相对路径，如 `uploads/xxx.jpg` |
| `file_size` | `bigint` | NOT NULL | 字节 |
| `mime_type` | `varchar(100)` | NOT NULL | 嗅探后确认的类型 |
| `media_type` | `varchar(20)` | NOT NULL | `image` / `video` / `audio` / `document` |
| `width` / `height` | `bigint` | DEFAULT `0` | 仅 jpeg/png/gif 有值 |

索引：`idx_homehub_media_assets_media_type`、`idx_homehub_media_assets_deleted_at`。

**评审要点**：`filename` 没有唯一约束；物理文件在 `web/static/uploads/`，
删记录不保证删文件。`media_type` 的取值由 `media_types.go` 的
`supportedFormats` 驱动，但列本身没有 CHECK 约束。

---

## 6. 日志

### 6.1 `homehub_log_entries`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `level` | `varchar(10)` | NOT NULL | DEBUG/INFO/WARN/ERROR |
| `message` | `varchar(2048)` | NOT NULL | 超长会被截断 |
| `source` | `varchar(255)` | | 调用源文件 |
| `fields` | `text` | | 附加字段，JSON 字符串 |
| `log_time` | `timestamptz` | NOT NULL | 事件时间（与 `created_at` 不同） |

索引：`idx_homehub_log_entries_level`、`idx_homehub_log_entries_log_time`、
`idx_homehub_log_entries_deleted_at`。

**评审要点**：没有任何自动清理。`log.db_writer=true` 时日志会无限增长，
是长期运行后最可能失控的表。建议加按 `log_time` 的保留天数和定期物理删除。

---

## 7. RSS 订阅

### 7.1 `homehub_rss_groups`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `name` | `varchar(100)` | NOT NULL | |
| `sort_order` | `bigint` | DEFAULT `0` | |

### 7.2 `homehub_rss_feeds`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `title` | `varchar(255)` | NOT NULL | 抓取时自动更新 |
| `url` | `varchar(2048)` | NOT NULL, **UNIQUE** | feed 地址 |
| `site_url` | `varchar(2048)` | | 站点主页 |
| `description` | `text` | | |
| `icon_url` | `varchar(2048)` | | 站点图标 |
| `fetch_interval` | `bigint` | DEFAULT `3600` | 秒 |
| `last_fetch` | `timestamptz` | | 最近抓取时间 |
| `error_msg` | `varchar(1024)` | | 最近一次错误，超长截断 |
| `group_id` | `bigint` | NULL 表示未分组 | |

索引：`idx_homehub_rss_feeds_url`（unique）、`idx_homehub_rss_feeds_group_id`、
`idx_homehub_rss_feeds_deleted_at`。

### 7.3 `homehub_rss_entries`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `title` | `varchar(500)` | NOT NULL | |
| `link` | `varchar(2048)` | NOT NULL, **UNIQUE（全表）** | 条目链接 |
| `author` | `varchar(255)` | | |
| `content` | `text` | | 清洗后的 HTML |
| `magnet_url` | `text` | | 从附件/正文提取 |
| `published` | `timestamptz` | | 源给出的时间 |
| `fetched_at` | `timestamptz` | NOT NULL | 本地抓取时间 |

索引：`idx_homehub_rss_entries_link`（**unique，全表**）、
`idx_homehub_rss_entries_feed_id`、`idx_homehub_rss_entries_published`、
`idx_homehub_rss_entries_deleted_at`，以及 pg_trgm 索引
`idx_rss_entries_title_trgm(title)`。

**评审要点**：`link` 全局唯一是**刻意的**。条目是纯内容、跨源共享；
归属不在这张表上，而在 `homehub_rss_entry_feeds`。同一篇文章被 N 个源
收录时只有一行内容，`content` 不重复存储。

`feed_id` 列在旧版本中存在，改造后不再写入；列保留（nullable）以便回滚，
可以用 `DROP COLUMN` 清掉，见 §10。

### 7.4 `homehub_rss_entry_feeds`

条目与订阅源的收录关系，条目归属的**唯一事实源**。

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `entry_id` | `bigint` | NOT NULL | 指向 `homehub_rss_entries.id` |
| `feed_id` | `bigint` | NOT NULL | 指向 `homehub_rss_feeds.id` |
| `published` | `timestamptz` | | **该源**给出的发布时间 |
| `first_seen_at` | `timestamptz` | NOT NULL | 首次被该源收录 |
| `last_seen_at` | `timestamptz` | NOT NULL | 最近一次出现在该源 |

索引：`idx_rss_entry_feed`（**unique**，`entry_id, feed_id`）、
`idx_homehub_rss_entry_feeds_feed_id`、`idx_homehub_rss_entry_feeds_published`、
`idx_homehub_rss_entry_feeds_deleted_at`。

**评审要点**：

- `retention_count` 的计数单位是这张表的行数，不是条目表。每个源各自
  保留自己的收录份额。
- `published` 放在这里而不是条目上：同一篇文章在两个源可能给出不同时间。
- 删除条目时先删关联、再判定孤儿。孤儿判定必须查"当前是否还有关联行"，
  不能用删除前的候选集。

### 7.5 `homehub_rss_images`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `cache_key` | `varchar(64)` | NOT NULL, **UNIQUE** | `md5(源 URL)` 十六进制 |
| `source_url` | `text` | NOT NULL | 原始图片 URL（回填的历史记录为空串） |
| `filename` | `varchar(255)` | NOT NULL | 缓存目录内文件名 |
| `content_type` | `varchar(100)` | | |
| `size_bytes` | `bigint` | NOT NULL, DEFAULT `0` | |
| `sha256` | `varchar(64)` | | 历史文件为空 |
| `last_seen_at` | `timestamptz` | NOT NULL | 最近被引用的时间，清理依据 |

索引：`idx_homehub_rss_images_cache_key`（unique）、
`idx_homehub_rss_images_last_seen_at`、`idx_homehub_rss_images_deleted_at`。

**评审要点**：`filename` 没有唯一约束，但 `cache_key` 唯一，而文件名由
`cache_key` 唯一决定，实际不会重复。物理文件在
`web/static/uploads/rss_img/`，与 `homehub_media_assets` 共目录但不共表。

### 7.6 `homehub_rss_image_refs`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `image_id` | `bigint` | NOT NULL | 指向 `homehub_rss_images.id` |
| `entry_id` | `bigint` | NOT NULL | 指向 `homehub_rss_entries.id` |

索引：`idx_rss_image_ref`（**unique**，`image_id, entry_id`）、
`idx_homehub_rss_image_refs_entry_id`、`idx_homehub_rss_image_refs_deleted_at`。

**评审要点**：

- 引用指向条目而不是源：条目已跨源共享，图片因此只有一份引用。删掉 A 源
  而 B 仍在收录同一条目时，图片引用不受影响。
- 旧版本用 `(image_id, feed_id, entry_hash)`：`entry_hash` 是为了绕开
  2048 字符 link 进联合索引的限制，改成 `entry_id` 后不需要了。
- `reconcileRefs` 定期删除没有对应条目的引用（一条 `NOT EXISTS`）。

---

## 8. 待办与剪贴板

### 8.1 `homehub_todos`

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `id` | `bigserial` | PK | |
| 公共列 | | | |
| `title` | `varchar(500)` | NOT NULL | |
| `description` | `text` | | |
| `status` | `varchar(20)` | NOT NULL, DEFAULT `'pending'` | 见下 |
| `priority` | `bigint` | DEFAULT `0` | 越大越靠前 |
| `due_date` | `timestamptz` | 可空 | |
| `sort_order` | `bigint` | DEFAULT `0` | |

`status` 合法值：`planned` / `pending` / `in_progress` / `completed` / `paused`。
应用层 `model.IsValidTodoStatus` 校验，**数据库没有 CHECK 约束**。

索引：`idx_homehub_todos_status`、`idx_homehub_todos_deleted_at`。

**评审要点**：待办是**全体用户共享**的，表上没有 `user_id`。这是产品决策，
不是遗漏；但多用户场景下无法做到人各有份。

### 8.2 `homehub_pastes`

这张表**不经过 AutoMigrate**，由 `model.CreatePastesTable` 手工建表。
原因是业务层用物理删除，而 AutoMigrate 会按 `BaseModel` 生成一套列定义；
显式 DDL 让 `deleted_at` 的存在理由（与模型对齐）写在建表语句旁边。

```sql
CREATE TABLE IF NOT EXISTS homehub_pastes (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    public_id VARCHAR(12) NOT NULL UNIQUE,
    content TEXT NOT NULL,
    delete_token VARCHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ
)
```

| 列 | 说明 |
|----|------|
| `public_id` | 12 字符随机 ID（`model.NanoID`，字母数字），出现在 URL 里，带 UNIQUE（索引名由 PostgreSQL 默认生成为 `homehub_pastes_public_id_key`） |
| `content` | 正文，上限由 `pastebin.max_content_length` 控制（默认 1MB） |
| `delete_token` | 删除凭证，只返回一次 |
| `expires_at` | 过期时间，到期后物理删除 |

**评审要点**：与其它表不同，这里**没有**为 `expires_at` 或 `deleted_at` 建索引，
过期清理是 `WHERE expires_at < now()` 全表扫描。数据量小时无所谓，长期运行
建议加 `CREATE INDEX ON homehub_pastes (expires_at)`。

`delete_token` 没有唯一约束，也没有索引；删除操作走 `public_id` 定位后再比对
token，因此不需要额外索引。

---

## 9. 条目归属模型（已定：内容表 + 关联表）

### 9.1 为什么不是复合唯一 `(feed_id, link)`

早期考虑过把 `link` 的唯一约束改成 `(feed_id, link)`。那只是"每个源各存
一份副本"：同一篇文章在 N 个源下存 N 行，正文和图片引用都重复 N 份，
跨源去重完全失效。

最终采用**内容表 + 关联表**：

```
homehub_rss_entries        纯内容，按 link 全表唯一
homehub_rss_entry_feeds    哪个源收录了哪个条目 + 该源的发布时间
```

一条内容只存一份，被 N 个源指向。

### 9.2 删除语义（设计的核心）

需求是：**删 A 源不能带走 B 源仍在用的内容**。三道保证：

1. `DeleteFeed` 只删 `homehub_rss_entry_feeds WHERE feed_id = A`，
   不碰 `homehub_rss_entries`；
2. 删完关联后用 `NOT EXISTS (SELECT 1 FROM homehub_rss_entry_feeds ...)`
   判定孤儿 —— 只要 B 还收录，条目就不是孤儿，一行都不会删；
3. 图片引用挂在条目上（`entry_id`），条目还在 → 引用还在 → 图片不会被
   `Sweep` 回收。

`retention_count` 同理：计数单位是"本源收录了多少条"，只删本源的关联行。

### 9.3 孤儿条目的回收时机

- 删除订阅源之后（同一事务内）
- 每次 retention 清理之后（同一事务内）
- 之后触发一次 `Sweep` 回收失去引用的图片文件

孤儿条目用**物理删除**：软删除会让 `link` 唯一索引一直占位，同一篇文章
日后重新出现时插入会被 `ON CONFLICT` 静默吞掉。

### 9.4 正文冲突策略

同一链接两个源可能给出不同正文。当前是**首次写入优先**：条目第一次入库
时清洗后的正文被保留，后续源抓到时只更新 `magnet_url`，不覆盖正文。

理由：重复清洗会重新下载一遍图片（即使命中文件缓存），而且摘要覆盖全文
是不可逆的信息损失。

### 9.5 其他已知项

| 表 | 问题 | 影响 |
|----|------|------|
| `homehub_log_entries` | 无自动清理 | 长期运行后无限增长 |
| `homehub_pastes` | `expires_at` 无索引 | 过期清理全表扫描 |
| `homehub_users` | `username` 唯一索引含软删除行 | 删除后用户名不可复用 |
| `homehub_media_assets` | `filename` 无唯一约束 | 理论上可重复，实际由 UUID 保证 |
| `homehub_todos` | `status` 无 CHECK 约束 | 非法值只能靠应用层拦截 |
| `homehub_rss_entries` | 旧 `feed_id` 列保留为 NULL | 迁移后可 `DROP COLUMN`；不删也无害 |

---

## 10. 迁移脚本（需要手工执行，不在启动流程里）

`ddl_guard_test.go` 把 `DROP TABLE` / `DROP INDEX` / `ALTER TABLE` 全部
判为违规，因此任何破坏性变更都必须由人执行。启动时会检查迁移状态，未完成
则拒绝启动（`internal/database/schema_guard.go`）。

### 10.1 本次迁移（001）

完整脚本：`docs/migrations/001-rss-shared-entries.sql`

```bash
psql -h <host> -U <user> -d homehub -f docs/migrations/001-rss-shared-entries.sql
```

脚本做四件事：

1. 建 `homehub_rss_entry_feeds` 及其索引；
2. 把现有 `entries.feed_id` 回填成关联行；
3. 给 `homehub_rss_image_refs` 加 `entry_id`，按 `feed_id + sha256(link)`
   反查回填，换掉旧的三列唯一索引；
4. 把三处旧列改为允许 NULL（不删列，便于回滚）。

**前置要求**：PostgreSQL 11+（用到内置 `sha256(bytea)`）。低于 11 时第 3 步
会报错，此时改为：

```sql
DELETE FROM homehub_rss_image_refs;
```

图片文件不受影响；应用启动后 `BackfillLegacy` 会按条目正文重建引用。

**数据不可恢复项**：迁移前，两个源收录同一链接时只有一个源能看到条目
（另一条被 `ON CONFLICT` 吞掉）。迁移只搬运现有行，无法还原当年丢失的
归属；那些源需要重新抓取一次才会补上关联。

### 10.2 迁移完成后的可选清理

确认稳定后可以删掉不再使用的旧列（不可逆，先备份）：

```sql
ALTER TABLE homehub_rss_entries DROP COLUMN feed_id;
ALTER TABLE homehub_rss_image_refs DROP COLUMN feed_id, DROP COLUMN entry_hash;
```

删掉之后 `schema_guard.go` 的检查会自然通过（列不存在 = 不需要迁移）。

### 10.3 如果将来还要改

新增手工迁移时：

1. 在 `docs/migrations/` 加编号 SQL；
2. 在 `internal/database/schema_guard.go` 的 `schemaRequirements` 里加一条
   检查（只描述缺什么，SQL 留在 .sql 文件里，避免被 DDL 守卫误判）；
3. 同步本文档。
