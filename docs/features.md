# 功能手册

各内置模块的行为、配置与接口。API 的请求/响应结构见
[swagger.yaml](swagger.yaml)（运行时 `/swagger/index.html`），本文只讲
"怎么用、为什么这样设计、有哪些坑"。

模块清单：

- [剪贴板 Pastebin](#剪贴板-pastebin)
- [媒体资源](#媒体资源)
- [书签](#书签)
- [RSS 订阅](#rss-订阅)
- [待办事项](#待办事项)
- [LLM 余额监控](#llm-余额监控)
- [全局搜索](#全局搜索)
- [系统日志](#系统日志)
- [认证与用户](#认证与用户)

---

## 剪贴板 Pastebin

公开的临时文本分享。**无需登录**即可创建、读取、删除。

### 设计取舍

- **公开写入**：任何人都能创建，因此必须靠 TTL 自动清理，不能无限增长。
- **物理删除**：`PurgeExpired` 用 `Unscoped()` 做真实 `DELETE`。
  `Paste` 模型嵌入 `BaseModel` 因而带 `deleted_at` 列，若用普通 `Delete`
  只会写入软删除标记，过期数据永不清理、表会持续膨胀。
- **删除需密钥**：`delete_token` 在创建时一次性返回，删除必须提供。
  `public_id` 会出现在 URL 和粘贴列表里，单凭它无法证明请求者是创建者。

### 配置

`config.json` → `pastebin`：

| 字段 | 默认 | 说明 |
|------|------|------|
| `enabled` | true | 模块总开关（键缺失时视为 true） |
| `max_content_length` | 1048576 | 单条内容上限（字节），范围 1–10MB |
| `default_ttl_hours` | 168 | 默认存活时间（7 天），范围 1–8760 |
| `cleanup_interval_hours` | 24 | 过期清理间隔，范围 1–168 |

关闭模块后所有接口返回 **403**，但清理循环仍会运行，把历史遗留数据清掉。

### 接口

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/api/v1/pastes` | 无 | 最近 50 条摘要（不含正文与密钥） |
| POST | `/api/v1/pastes` | 无 | 创建，返回 `id` 与 `delete_token` |
| GET | `/api/v1/pastes/{id}` | 无 | 读取正文；过期返回 404 |
| DELETE | `/api/v1/pastes/{id}` | 无 | 删除，**必须带密钥** |

删除密钥的两种传法（后者避免 token 进入访问日志的 URL）：

```bash
# query
curl -X DELETE "http://localhost:8080/api/v1/pastes/abc123?token=THE_TOKEN"

# header
curl -X DELETE -H "X-Paste-Delete-Token: THE_TOKEN" \
  http://localhost:8080/api/v1/pastes/abc123
```

### 创建与读取

```bash
# 创建
curl -X POST http://localhost:8080/api/v1/pastes \
  -H 'Content-Type: application/json' \
  -d '{"content":"hello"}'
# → {"code":0,"data":{"id":"JcPUwuTu","content":"hello","delete_token":"..."}}

# 读取（浏览器打开 /paste/{id} 也可以）
curl http://localhost:8080/api/v1/pastes/JcPUwuTu
```

### 前端行为

`/paste` 页面创建粘贴后，会把 `delete_token` 存进当前浏览器的
localStorage（键名 `homehub.paste.delete.{id}`）。因此在**同一浏览器**
里能看到删除按钮，换设备则没有 —— 这与粘贴的临时性质一致。

密钥错误与"粘贴不存在"统一返回 404，避免被用来探测哪些 ID 存在。

---

## 媒体资源

上传图片/视频/音频/PDF，用于首页与登录页动态背景，也可作为普通文件库。

### 支持的文件类型

单一事实源在 `internal/service/media_types.go` 的 `supportedFormats`，
上传校验、MIME 推断、类型归类都由它驱动。

| 类别 | 扩展名 |
|------|--------|
| image | `.jpg` `.jpeg` `.png` `.gif` `.webp` `.bmp` `.avif` `.ico` `.tif` `.tiff` `.svg` |
| video | `.mp4` `.m4v` `.webm` `.ogv` `.mov` `.mkv` `.avi` |
| audio | `.mp3` `.m4a` `.aac` `.ogg` `.oga` `.opus` `.wav` `.flac` |
| document | `.pdf` |

扩展方式：在 `supportedFormats` 加一行即可，无需改其他代码。

### 上传校验

两道关：

1. **扩展名白名单** —— 不在表里直接拒绝。
2. **内容嗅探** —— 读取文件头 512 字节，用 `http.DetectContentType`
   检测真实 MIME，与扩展名登记的类别比对。改扩展名伪装会被拦下。

以下格式跳过内容嗅探，因为嗅探结果不可靠（`svg` → `text/xml`，
`mkv`/`avi`/`flac` → `application/octet-stream`），改为靠响应头兜底。

### 安全措施

`/static/uploads/` 下的响应带 `X-Content-Type-Options: nosniff`。
SVG 额外加：

```
Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src data:
X-Frame-Options: DENY
```

原因：SVG 是 XML，可以内嵌 `<script>`。用户上传的 SVG 若被直接访问
会执行脚本（会话 Cookie 是 HttpOnly 的，但同源 fetch 仍能冒充用户
操作）。加 CSP 后它只能作为图片渲染。

PDF 带 `Content-Disposition: inline`，由浏览器内建阅读器打开。

### 图片尺寸

上传图片时会读像素尺寸写入 `width`/`height`。只对 Go 标准库内置解码器
（jpeg/png/gif）生效，`webp`/`avif`/`svg` 留 0，前端会省略尺寸显示。

### 限制

- 单文件上限 **50MB**（`maxUploadSize`）
- 文件名重写为 UUID，原始名仅存数据库
- 物理文件在 `web/static/uploads/`，数据库只存元数据

---

## 书签

分组 + 链接管理，带图标抓取与代理支持。

- **排序**：分组与书签都支持上移/下移（`POST /bookmarks/reorder`），
  不是拖拽。
- **图标**：`icon` 字段可为空字符串（允许清空）。抓取走 `favicon_service`，
  可通过 `bookmark.proxy_names` 指定代理链。
- **归属**：书签必须属于某个分组，删除分组会级联删除其下书签。

---

## RSS 订阅

### 抓取模型

三层：

```
后台调度（每 5s 检查）
  └─ FetchAll：并发抓取到期的订阅源（fetch_concurrency）
       └─ FetchFeed：单源抓取（fetch_timeout_seconds 预算）
            └─ sanitizeContent：并发缓存条目内图片（image_cache_concurrency）
```

关键点：**单个源超时只占用一个并发槽**，不会拖住整轮。旧实现是串行的，
N 个源各超时 30s 会让后台循环彻底堵死。

### 配置

`config.json` → `rss`：

| 字段 | 默认 | 说明 |
|------|------|------|
| `retention_count` | 5000 | 每个源保留条目数 |
| `background_fetch_enabled` | true | 是否后台定时抓取 |
| `background_fetch_interval` | 300 | 轮询间隔（秒），改动 5 秒内生效 |
| `fetch_concurrency` | 4 | 同时抓取的源数，1–32 |
| `fetch_timeout_seconds` | 30 | 单源预算（秒），5–600 |
| `image_cache_enabled` | true | 是否本地缓存条目图片 |
| `image_cache_concurrency` | 6 | 单条目图片并发数，1–32 |
| `proxy_names` | — | 代理链，按优先级 |
| `user_agent` | Chrome UA | 自定义 UA |

代理与 UA 变更会在下次抓取时重建 HTTP 客户端，无需重启。

### 图片缓存

抓取耗时的主要来源是条目内嵌图片。缓存开启时：

1. 收集条目内所有外链图片，按 URL 去重
2. 并发下载（受该源的超时预算约束）
3. 改写为 `/static/uploads/rss_img/<md5>.<ext>`
4. 下载失败的图片标签整体移除

正文清洗走白名单 DOM 重建（不是正则）：`script`/`iframe`/`svg` 等标签连同
内容删除，未登记的标签去掉标签保留文字，`on*`/`style`/`srcset` 等属性一律
丢弃，`javascript:`/`vbscript:`/协议相对 URL 被拒绝；`target="_blank"` 自动
补 `rel="noopener noreferrer"`。

`image_cache_enabled=false` 时完全跳过，图片保留原始外链由浏览器加载。
**如果源站图片本身可直连，这是最省事的提速手段。**

### 已知限制

- 重新抓取只更新 `magnet_url`（`ON CONFLICT DO UPDATE`），**不会重建
  content**。所以历史条目的内嵌图片若被删除，重新抓取也不会恢复。
- 单个源失败只记 `rss_feeds.error_msg`，下一轮按 `fetch_interval` 重来，
  没有指数退避。

### 交给外部服务

抓取实现被抽成 `service.RSSFetcher` 接口，可通过 `SetFetcher` 替换。
契约与接入方式见 [extension.md](extension.md)。

---

## 待办事项

所有登录用户**共享**同一个任务集合（不是每人一份）。

- 状态：`planned` / `pending` / `in_progress` / `completed` / `paused`，
  由 `model.IsValidTodoStatus` 校验
- 排序：`POST /todos/reorder`
- 状态切换走 `PATCH /todos/{id}/status`

---

## LLM 余额监控

按固定间隔查询各平台 API 余额，结果**只存内存**，不入库。

- 目前支持的 `type`：`deepseek`
- 每个 target 的 `api_key` **必填**（与 `enabled` 无关，缺失会导致配置校验失败）
- `refresh_interval_seconds` 范围 10–3600
- 前端用 Chart.js 饼图展示

配置：

```json
"balance_monitor": {
  "enabled": true,
  "refresh_interval_seconds": 60,
  "targets": [
    {"name": "DeepSeek", "type": "deepseek", "base_url": "https://api.deepseek.com",
     "api_key": "sk-...", "enabled": true}
  ]
}
```

---

## 全局搜索

`GET /api/v1/search?q=关键词&scope=all|bookmarks|rss`

- 使用 `ILIKE '%关键词%'`，依赖 pg_trgm GIN 索引加速
- 索引由 `database.EnsureSearchIndexes` 在启动时自动创建；
  扩展不可用（账号无 `CREATE EXTENSION` 权限）时只记警告，
  搜索退化为全表扫描
- 每个数据源最多返回 10 条

---

## 系统日志

- 控制台 + 数据库双写（`log.db_writer` 控制后者）
- 日志面板通过 SSE 实时推送：`GET /api/v1/admin/logs/stream`
- 数据库写入是异步批量（缓冲区满时丢弃，不阻塞主流程）
- 仅管理员可访问

---

## 认证与用户

- 登录：用户名 + 密码（bcrypt），成功后发 `homehub_session` Cookie
- 会话存数据库，每小时清理过期记录
- 角色：`admin` / `user`。`/admin/*` 与 `/config` 需要管理员
- 头像：由邮箱对应的 Gravatar 生成，缓存在数据库。
  `gravatar.enabled=false` 时接口返回 404，前端降级为首字母占位
- 初始管理员由 `config.json` 的 `auth.password` 创建，创建后该字段被清空
