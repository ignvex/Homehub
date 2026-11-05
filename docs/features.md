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

## 媒体资源（图床）

上传图片/视频/音频/PDF，作为**图床**使用：文件通过
`/static/uploads/<filename>` 公开可访问，可直接用于首页与登录页动态
背景，也可贴到外部站点。

**与 RSS 抓取的图片分开**：本模块只管用户上传的资产（
`media_assets`）；RSS 图片是另一张表（`rss_images`）
与另一个子目录（`uploads/rss_img/`），因为它属于可回收的抓取缓存。
数据库记录带 `source` 字段（当前恒为 `upload`），为将来统一展示预留。

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
- 文件名重写为 UUID，原始名仅存数据库（超长按字符截断，不会切断中文）
- 物理文件在 `web/static/uploads/`，数据库只存元数据
- 上传时记录内容 `sha256`，用于完整性校验；**尚未做自动去重**
- 删除是物理删除（记录 + 文件），软删除对图床资产没有意义

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
3. **按内容哈希去重**：下载完后算 SHA256，同一份内容已存在则复用，
   不再落盘
4. 改写为 `/static/uploads/rss_img/rss_<时间戳>_<随机>_<哈希前8>.<ext>`
5. 下载失败的图片标签整体移除

**为什么按内容而不是 URL 去重**：同一张图常以多个 URL 出现——CDN 签名
参数不同、转载站换域名、时效 token 每轮都变。按 URL 去重时这三种情况
都会重复存储，图床/OSS 的空间就是这样涨上去的。

**为什么文件名带时间与随机**：`<时间戳>_<随机>` 让文件不可枚举且不随
时间重复，`<哈希前8>` 便于排障时反查内容。唯一性由数据库的
`cache_key`（内容 SHA256）保证，文件名不承担 identity 职责。

**URL → 内容 的映射**存在 `rss_image_sources`。没有它，每轮
抓取都得重新下载才能算出内容哈希——那就变成“省磁盘赔带宽”了。

**登记失败时图片不会丢，而是整条正文推迟落库**。正文写入后不会重算
（只处理空正文），所以图片登记失败时返回的是空正文——下次抓取重新
处理。若写成“没有图的正文”，这批图片就永久丢失了。

**并发去重**：`contentMu` 串行化“算完内容哈希 → 决定落盘还是复用”
这一步。不加锁时并发下载同一内容（不同 URL、相同字节）会全部在
`lookupByContent` 里 miss（前一个的登记要等整批 `register`），
各自落一份文件。锁内只有 rename 与内存记录，数据已写入临时文件，
耗时很短。进程内的 `memContent`（内容哈希 → 结果）是这条路径的
复用入口，无数据库时它是唯一手段。

**清理不会被正在处理的下载误伤**：`Sweep` 看两层保护——进程内的
`pending` 引用计数（从下载完成到登记完成之间的窗口）和至少 1 分钟的
宽限期。用计数而非集合，是因为两个并发抓取批次可能解析到同一张图。

正文清洗走白名单 DOM 重建（不是正则）：`script`/`iframe`/`svg` 等标签连同
内容删除，未登记的标签去掉标签保留文字，`on*`/`style`/`srcset` 等属性一律
丢弃，`javascript:`/`vbscript:`/协议相对 URL 被拒绝；`target="_blank"` 自动
补 `rel="noopener noreferrer"`。

`image_cache_enabled=false` 时完全跳过，图片保留原始外链由浏览器加载。
**如果源站图片本身可直连，这是最省事的提速手段。**

### 条目归属

条目是**跨源共享的内容**：`rss_entries` 按 `link` 全表唯一，
"哪个源收录了它"记在 `rss_entry_feeds`。因此：

- 同一条内容被多个源收录时只存一份正文；
- 删除一个源只删它的关联行，其他源仍能看到这条内容；
- `retention_count` 按"本源收录数"计数，不影响其他源。

### 阅读端的分段加载

列表接口 `GET /rss/entries` 只返回 `excerpt`（300 字纯文本）与
`has_content`，不返回 `content`。正文由 `GET /rss/entries/:id/content`
在展开某条时单独取：

```
标题（列表一次请求）
  └─ 展开：正文 HTML（按条目 ID 单独请求）
       └─ 图片：滚进视口才设置 src
```

列表里只显示标题与元信息。摘要在 qB 式密列表中是多出来的一行：
标题本身已足以决定「要不要展开」，多两行反而把一屏能扫过的条数减半。
接口仍然下发 `excerpt`，供首页卡片与「有没有正文」的兜底判断使用。

摘要由服务端生成：按 DOM 解析，跳过 `script`/`style`/`svg` 等不可见
内容，折叠空白后按 **rune** 截断（300）。前端不再用 `DOMParser` 剥
HTML——那一步在首屏对 20 条正文做一遍，是白花的。

`has_content` 缺失时（前端先上线、后端还是旧版本），前端退回按
`content`/`excerpt` 是否存在推断，并把旧接口直接下发的 `content`
当作已加载正文。少这层兜底，两边版本错开的那一刻「展开正文」会
静默消失。

图片是第三段：正文 HTML 里的 `img` 的 `src` 被挪到 `data-src`，
`IntersectionObserver` 在图片接近视口（提前 200px）时写回。没有
`IntersectionObserver` 的环境退化为全部立即加载。

正文端点对已删除/被 retention 清理的条目返回 **404**。这是正常竞态：
列表刚加载完，条目可能就被清理了，不应回升成 500。

### 管理窗口

「订阅管理」按源下手：多选后批量移入分组、切换抓取方式、暂停恢复、
改间隔、删除。搜索只收窄可见范围，不清空已有选中——先筛一批再清空
搜索框是这类窗口的常规用法。

「分组管理」按组下手：每个分组可展开，列出组内订阅源并可移出；底部
下拉只列「还不在本组」的源。未分组单独占一个区块，因此任何源都有
落点。重命名是行内编辑，不叠第二个弹窗。

批量写入走 `PUT /rss/feeds/batch`：一次事务、一条 `UPDATE ... WHERE id IN`。
空 `ids` 在 service 层与 handler 层各拒一次，且拒绝时完全不碰数据库——
空列表若被放行，`IN` 条件退化后一次请求就能改光整张表。删除不走批量
端点：每条删除要处理条目归属与孤儿回收，逐条执行并汇总「成功几个、
失败几个」比合成一个大事务更诚实。

### 已知限制

- 重新抓取只更新 `magnet_url`（`ON CONFLICT DO UPDATE`），**不会重建
  content**。所以历史条目的内嵌图片若被删除，重新抓取也不会恢复。
- 同一链接被多个源收录时，正文取**首个**处理它的源；其他源只补充归属
  和发布时间，不会覆盖正文。
- 单个源失败只记 `rss_feeds.error_msg`，下一轮按 `fetch_interval` 重来，
  没有指数退避。

### 交给外部服务

抓取实现被抽成 `service.RSSFetcher` 接口，可通过 `SetFetcher` 替换。
契约与接入方式见 [extension.md](extension.md)。

### 磁力链接锁定与 PikPak

磁力链接从 `item.Enclosures` 或正文正则提取，存 `rss_entries.magnet_url`。

**锁定**保护这条链接不被后续抓取改写。抓取时的 upsert 有三层保护：

```
1. magnet_locked = TRUE        → 一个字都不改
2. 否则新值为空                 → 保留旧值
3. 否则                        → 采用新值
```

第 2 层（`COALESCE(NULLIF(...))`）早就有了，但它只挡得住空值：源站换了
磁力链接时仍会覆盖。第 1 层才是“锁定”真正做的事。

**PikPak 提交**把链接交给 PikPak 离线下载。要求**先锁定** —— 提交是外部
副作用（往第三方账号塞下载任务），不该对一条随时可能被改写的链接做。

职责边界刻意收得很窄：**只提交，不轮询、不管理文件**。HomeHub 记下任务
ID 与结果（`pikpak_status` / `pikpak_task_id`），之后 PikPak 那边怎么样
由 PikPak 自己管。这样即使上游改了接口，坏的也只是“提交”这一个动作。

配置（`rss.pikpak`）：

| 键 | 说明 |
|----|------|
| `enabled` | 默认 `false`。涉及第三方凭据，必须显式开启 |
| `endpoint` | 默认 `https://api-drive.mypikpak.com`（留空即用默认） |
| `username` / `password` | PikPak 账号 |
| `client_id` / `client_secret` | 由 PikPak 分配；留空则提交失败并提示 |
| `timeout_seconds` | 默认 15 |
| `auto_lock` | 默认 `false`。开启后抓取到的磁力链接自动锁定 |

`auto_lock` 默认关闭是有意的：“锁定”的价值在于**人工确认**，自动锁定
会让它退化成一个无意义的布尔值。开启它的场景是“我信这个源，凡是带磁力的
都算确认过”。自动锁定的 `magnet_locked_by` 记为 `0`，与人工锁定区分。

> ⚠️ 真实实现基于 PikPak 公开的 web API（signin + files），该接口非官方
> 文档化、可能随时变更。因此全部逻辑走 `PikPakClient` 接口可注入替身，
> 失败只记录、不重试、不阻塞抓取。

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
- `allow_private_hosts`（默认 false）：默认在连接层拒绝内网/回环/链路本地
  地址，防止借余额查询探测内网；自建中转在内网时再显式打开
- 查询失败时错误信息只含 HTTP 状态码，对方响应体只写 debug 日志
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

`GET /api/v1/search?q=关键词&scope=all|bookmarks|rss`（需登录）

- 使用 `ILIKE '%关键词%'`，依赖 pg_trgm GIN 索引加速
- 索引由 `database.EnsureSearchIndexes` 在启动时自动创建；
  扩展不可用（账号无 `CREATE EXTENSION` 权限）时只记警告，
  搜索退化为全表扫描
- 每个数据源最多返回 10 条

---

## 系统日志

- 控制台 + 数据库双写（`log.db_writer` 控制后者）
- 数据库日志保留 `log.retention_days` 天（默认 30，0 = 不限），
  启动时与之后每 6 小时清理一次
- 日志面板通过 SSE 实时推送：`GET /api/v1/admin/logs/stream`
- 数据库写入是异步批量（缓冲区满时丢弃，不阻塞主流程）
- 2 级（operator）及以上可访问

### 级别过滤

三种写法，优先级依次生效：

| 参数 | 语义 | 示例 |
|------|------|------|
| `levels` | 多选，逗号分隔 | `levels=ERROR,WARN` |
| `min_level` | 下限（含自身） | `min_level=WARN` → WARN + ERROR |
| `level` | 单个精确值（旧参数） | `level=ERROR` |

级别严重度序：`DEBUG < INFO < WARN < ERROR`。用它把“至少 WARN”翻译
成一组 `IN` 值，不必为每个组合手写条件。

**非法级别返回 400 并列出可选值**，不静默忽略。这一点很重要：
早先的实现里 `level=FOO` 会被跳过、不加任何 `WHERE`，接口返回全部日志 ——
调用方以为过滤生效了，实际没有。排查线上问题时，这种“看起来对但没生效”
比报错危险得多。

### 关键词搜索

`q=` 走 `message ILIKE '%...%'`，由 `idx_log_entries_message_trgm`
（pg_trgm GIN）支撑。日志表只增不减，是全库最可能变大的表，
这个索引的收益随时间线性增长。

---

## 认证与用户

- 登录：用户名 + 密码（bcrypt），成功后发 `session` Cookie
- 会话存数据库，每小时清理过期记录（物理删除，避免表膨胀）
- 头像：由邮箱对应的 Gravatar 生成，缓存在数据库。
  `gravatar.enabled=false` 时接口返回 404，前端降级为首字母占位
- 初始管理员由 `config.json` 的 `auth.password` 创建，创建后该字段被清空

### 5 级权限

| 值 | 名称 | 能力 |
|----|------|------|
| 1 | admin | 配置、用户管理、日志、全部内容 |
| 2 | operator | 用户管理（不含 1 级）、日志查看与清理 |
| 3 | editor | 全部内容的增删改，不分作者 |
| 4 | member | 只能改自己创建的内容 |
| 5 | viewer | 只读 |

**数值越小权限越高。** 鉴权就是 `user.Level <= required`，没有角色映射表，
也没有“哪个角色能干什么”散落在各处。

路由分组对应关系：

| 路径前缀 | 最低等级 |
|----------|---------|
| `/config/*` | 1 |
| `/admin/*` | 2（具体到某账号再比一次等级） |
| `/bookmarks/*`、`/rss/*` 写、`/todos/*` 写、`/background` | 3 |
| `/media-assets` 写 | 4（再校验 `owner_id`） |
| 其余只读 | 登录即可 |

### 防提权

三条规则，全部写进 service 层而不是依赖 handler 自觉：

1. **只能创建严格低于自己等级的账号**。2 级不能造 1 级（否则每个中间
   等级都是一条直达最高权限的路），也不能造同级（否则可以复制同类账号）。
2. **只能修改/删除/重置严格低于自己等级的账号**。同级之间不能互相停用。
3. **任何人不能改自己的等级或启用状态**。改自己的密码走
   `ChangePassword`，那里要验证旧密码 —— 否则任何“能管自己”的路径
   都等于免密改密。

### 反向代理与客户端 IP

登录失败锁定与匿名粘贴限流都按客户端 IP 计数。IP 的来源由
`server.trusted_proxies` 决定：

```json
"server": {
    "host": "0.0.0.0",
    "port": 8080,
    "trusted_proxies": ["172.17.0.1", "10.0.0.0/8"]
}
```

- **默认为空：不信任任何转发头**，客户端 IP 一律取 TCP 直连地址。
  直接暴露在公网、没有反代时保持为空即可
- **部署在 Nginx / Caddy / Traefik / Unraid 反代后面时必须配置**，填反代
  到达 HomeHub 时的源地址（IP 或 CIDR）。不配的后果是所有请求看起来都
  来自反代：一个人输错密码就会锁住所有人
- 只有直连地址落在列表内时才读取 `X-Forwarded-For` / `X-Real-IP`；
  其他来源伪造这两个头不起作用。Gin 默认信任所有来源，那样换个头
  就能绕过限流与锁定，所以这里反过来默认谁都不信
- 每项必须是合法 IP 或 CIDR，否则启动时配置校验失败

### CSRF 防护

两道独立防线，任何一道命中即拒绝：

1. **Origin / Referer 校验**（主力）。浏览器对跨站发起的非安全请求
   一定带 `Origin`，与本站 `Host` 不符即判定跨站。这条不需要前端配合。
2. **双提交 Cookie**（加强）。服务端下发随机 token 到非 HttpOnly 的
   `homehub_csrf` cookie；客户端若在 `X-CSRF-Token` 头里带回同样的值，
   再比对一次。挡的是“Origin 被伪造”的场景。

**两者都缺失时放行**：curl、脚本、服务端到服务端调用不会带 `Origin`，
它们也不具备浏览器“自动附带 cookie”的特性，因此不存在 CSRF 风险。
如果连这种情况也拒掉，会把所有非浏览器客户端一并打死，安全上没有任何收益。

只比 `host:port`，不比 scheme —— 反向代理终结 TLS 时，应用看到的
scheme 与浏览器不同（http vs https），比 scheme 会误杀所有生产请求。
`Origin: null`（沙箱 iframe、file://）一律拒绝。

### 会话

- token 用 `crypto/rand` 取 32 字节（256 位），存库比对
- Cookie 是 `HttpOnly`；CSRF token 那个**不是**（前端要读）
- 登录后会话 7 天有效；改密、被停用、被删号时全部会话立即失效
- 停用账号会在中间件里被拦下 —— 登录时只查一次 `is_active`，
  会话本身不带启用状态，不在这里拦就能一直用到过期
