# HomeHub Extension 接入（抓取与头像代理）

> **⚠️ 本文描述的 `/fetch` 与 `/avatar` 端点在当前 Extension 上尚未实现。**
>
> 2026-11 实测 `https://www.ignvex.cc`：`/fetch` 与 `/avatar/{hash}` 都返回
> `200 text/html` 的 SPA 首页（630 字节），真实存在的公开端点是 `/healthz`
> 与 `/readyz`（JSON）。
>
> 因此 `extension.features.fetch` 与 `extension.features.avatar` 默认关闭。
> 打开它们之前，先确认对端真的实现了这两个端点——HomeHub 会把 HTML 当成
> 错误拦下并明确说「返回的是 HTML 而不是 feed」，但排查仍然要花时间。
>
> 现在可用的是 `/hub/*` 签名通道（`extension.features.hub`），见
> [hub-integration.md](hub-integration.md) 与 [extension-hub.md](extension-hub.md)。
>
> 本文作为**契约参考**保留：要实现对端代理能力时，按下面的协议做。

`homehub-extension` 是独立部署的外部资源代理服务。本文定义它与 HomeHub 的
通信契约，曾经包含两类能力：

- **RSS 抓取**（`/fetch`）：HomeHub 把订阅源抓取交给它
- **Gravatar 头像代理**（`/avatar/{hash}`）：HomeHub 把头像下载交给它

两类能力可以部署在同一个服务里，也可以只实现其中一类 —— 配置留空即回落到
HomeHub 本地实现。

## 通信方向：单向，HomeHub 发起

**HomeHub 是唯一发起方。** extension 不连接 HomeHub、不持有数据库凭据、
不推送数据。

```
HomeHub ──GET {base}/fetch?url=...────────▶ homehub-extension
        ◀──── 原始 RSS/Atom XML 或 JSON ────────────

HomeHub ──GET {base}/avatar/{hash}?s=...──▶ homehub-extension
        ◀──── 头像二进制 + ETag/Last-Modified ──────
        ◀──── 304 Not Modified（未变化）────────────
```

这条约束带来几个好处，也是刻意设计的：

- extension 挂了只影响抓取速度，不会拖垮登录、书签、待办等模块
- extension 无需暴露入站端口给 HomeHub 以外的任何调用方
- 不需要服务间双向鉴权，只需 HomeHub 持有调用凭据
- 拓扑简单：extension 可以放在网络条件更好的位置（境外 VPS、NAS 等）

**不要**让 extension 直连 HomeHub 的数据库写表。表结构由 AutoMigrate
管理，外部写入会让 schema 演进失去唯一事实源，也可能破坏无关数据。

## 已实现的契约

### 请求

```
GET {base}/fetch?url=<URL 编码的订阅源地址>
Accept: application/xml, application/rss+xml, application/atom+xml, application/json
X-HH-Timestamp / X-HH-Nonce / X-HH-Signature     # Ed25519 签名，与 /hub/* 同一把密钥
```

- 超时由 HomeHub 侧 `rss.fetch_timeout_seconds` 控制（默认 30s）
- 不跟随重定向：extension 地址由运维配置，跳转不应发生
- extension 侧应自行完成代理、UA 伪装、重试与缓存

### 响应

成功返回 **200**，body 可以是以下任一形态：

**形态 A —— 原始 XML**（推荐，最简单）

```http
Content-Type: application/rss+xml
```
```xml
<?xml version="1.0"?>
<rss version="2.0"><channel><title>...</title><item>...</item></channel></rss>
```

**形态 B —— JSON 包装 XML**

```http
Content-Type: application/json
```
```json
{"feed": "<?xml version=\"1.0\"?><rss ...>"}
```

**形态 C —— gofeed 的 JSON 结构**

```json
{"title": "...", "items": [{"title": "...", "link": "..."}]}
```

HomeHub 按 `Content-Type` 与 body 首字符判断：非 JSON 走 XML 解析，
JSON 先试 `feed` 字段再试整体结构。

### 失败

非 2xx 视为抓取失败。HomeHub 会把状态码与响应前 512 字节写入
`rss_feeds.error_msg`（截断 1024 字节），下一轮按该源的
`fetch_interval` 重试。

```http
502 Bad Gateway
upstream timeout
```

## 配置

配置在 `config.json` → `extension`，与 `/hub/*` 共用同一份地址与密钥：

```json
"extension": {
  "enabled": true,
  "base_url": "https://ext.example.com/api/v1",
  "private_key_pem": "[redacted] PRIVATE KEY-----\n...",
  "features": { "hub": true, "fetch": true, "avatar": false },
  "proxy_names": []
}
```

| 字段 | 说明 |
|------|------|
| `features.fetch` | 为真时 RSS 抓取全部交给 `GET /fetch`，否则本地抓取 |
| `features.avatar` | 为真时头像下载全部交给 `GET /avatar/{hash}`，否则直连上游 |
| `proxy_names` | 与 Extension 连接专用的代理链，留空 = 直连 |

地址必须是完整的 `http(s)://host[:port]`，否则配置校验不通过。
改完即生效（下一次抓取就会用新地址），**无需重启**：
`autoFetcher` 按配置指纹重建底层实现。

## 实现示例

> 下面的示例只演示**取数与回传**，认证部分仍写成 Bearer——重构后
> `/fetch` 与 `/avatar` 已改为 Ed25519 签名，与 `/hub/*` 共用同一套。
> 验签实现见 [hub-integration.md](hub-integration.md) §3.4（Go）与 §3.5（TS）。

最小可用的 extension（Go）：

```go
package main

import (
    "io"
    "log"
    "net/http"
)

func main() {
    http.HandleFunc("/fetch", func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("Authorization") != "Bearer change-me" {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        feedURL := r.URL.Query().Get("url")
        if feedURL == "" {
            http.Error(w, "missing url", http.StatusBadRequest)
            return
        }

        req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, feedURL, nil)
        req.Header.Set("User-Agent", "HomeHub-Extension/1.0")

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadGateway)
            return
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusOK {
            http.Error(w, "upstream "+resp.Status, http.StatusBadGateway)
            return
        }

        w.Header().Set("Content-Type", "application/rss+xml")
        io.Copy(w, io.LimitReader(resp.Body, 32<<20))
    })

    log.Fatal(http.ListenAndServe(":8090", nil))
}
```

## 测试

HomeHub 侧有覆盖这些行为的测试（`internal/service/rss_remote_fetcher_test.go`）：

- 请求确实是 `GET /fetch?url=`，且带三个签名头
- XML、`{"feed": ...}`、gofeed JSON 三种响应都能解析
- 非 2xx 返回带状态码的错误
- 配置在 `local` / `remote` 之间切换即时生效

本地联调时把 `extension.base_url` 指向 `httptest` 服务器或临时进程即可。

## Gravatar 头像代理

### 启用方式

`config.json` → `gravatar`：

| 字段 | 说明 |
|------|------|
| `features.avatar` | 为真时头像交给 extension，否则直连官方 Gravatar |
| `timeout_seconds` | 单次上游请求超时，默认 10，范围 1-60 |
| `max_bytes` | 单张头像大小上限，默认 2MB，范围 64KB-10MB |

```json
"gravatar": {
  "enabled": true,
  "timeout_seconds": 10,
  "max_bytes": 2097152
}
```

改完即生效（下一次缓存未命中就用新地址），**无需重启**：
`GravatarService` 按配置指纹重建上游实现。

**不做自动回退。** `features.avatar` 打开后若 extension 不可用，头像接口返回
`502`，不会偷偷直连外网 —— 与 RSS 抓取保持同一策略，避免运维以为流量走了
extension、实际却绕过了它。

### 请求

```http
GET {base}/avatar/{hash}?s={size}&d={style}&r={rating}
Accept: image/png, image/jpeg, image/gif, image/webp
X-HH-Timestamp / X-HH-Nonce / X-HH-Signature     # Ed25519 签名
If-None-Match: {etag}                  # 服务端有缓存时携带
If-Modified-Since: {last_modified}     # 同上
```

- `{hash}` 是邮箱小写去空格后的 MD5 十六进制（40 位以内）。**HomeHub 只传哈希，
  不传明文邮箱**；extension 不应记录 `hash + IP` 这类可关联信息。
- `s` 来自 `gravatar.default_size`（16-512），同一时刻所有用户使用同一尺寸。
- `d` / `r` 透传 `default_style` / `rating`，取值见 `docs/features.md`。
- **不跟随重定向**：extension 地址由运维配置，跳转只可能是配置错误或劫持，
  HomeHub 会把 3xx 当作失败处理。

### 响应

**200**：头像二进制。

```http
Content-Type: image/png
ETag: "6a1b..."
Last-Modified: Wed, 21 Oct 2025 07:28:00 GMT
Cache-Control: public, max-age=86400
```

extension 必须把**上游真实的** `Content-Type`、`ETag`、`Last-Modified` 透传回来，
不要自行编造：HomeHub 会用 ETag 做增量刷新，写错会导致每次刷新都重新下载。

**304 Not Modified**：`If-None-Match` / `If-Modified-Since` 命中，无 body。
HomeHub 保留现有缓存。

**404**：该邮箱没有头像（例如上游对 `d=404` 的响应）。HomeHub 会清除本地缓存，
并在 5 分钟内对该用户使用负缓存，不再打上游。

**其他非 2xx**：一律视为上游故障，返回 `502`，**不写缓存**。

### HomeHub 侧的强制校验

以下检查发生在 HomeHub 内，extension 无法绕过 —— 即使 extension 被攻破或返回
错误内容：

- **大小上限**：超过 `max_bytes` 直接失败，不截断（截断会写坏图片并长期缓存）
- **魔数嗅探**：用 `http.DetectContentType` 判断真实类型，**不信任声明的
  `Content-Type`**；只接受 `image/jpeg`、`image/png`、`image/gif`、`image/webp`
- **拒绝 SVG**：SVG 是 XML，可内嵌 `<script>`，而头像接口会被浏览器直接打开，
  等同把上游内容变成同源可执行脚本
- **超时**：由 `timeout_seconds` 控制，与 extension 自身超时无关

因此 extension 返回 HTML 错误页（哪怕标成 `image/jpeg`）会被拒绝，不会被
写进数据库。

### 最小实现示例

```go
package main

import (
    "io"
    "net/http"
    "time"
)

func avatarHandler(w http.ResponseWriter, r *http.Request) {
    if r.Header.Get("Authorization") != "Bearer change-me" {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    // 直接把请求（含条件头）转发给官方 Gravatar
    upstream := "https://www.gravatar.com/avatar/" + r.PathValue("hash") +
        "?" + r.URL.RawQuery

    req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
    for _, h := range []string{"If-None-Match", "If-Modified-Since", "Accept"} {
        if v := r.Header.Get(h); v != "" {
            req.Header.Set(h, v)
        }
    }

    client := &http.Client{Timeout: 8 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()

    for _, h := range []string{"Content-Type", "ETag", "Last-Modified"} {
        if v := resp.Header.Get(h); v != "" {
            w.Header().Set(h, v)
        }
    }
    w.WriteHeader(resp.StatusCode)
    io.Copy(w, io.LimitReader(resp.Body, 4<<20))
}
```

### 测试

HomeHub 侧覆盖这些行为（`internal/service/gravatar_source_test.go`、
`internal/handler/avatar_test.go`）：

- 请求形状：`GET /avatar/{hash}?s=&d=&r=`、Ed25519 签名头
- 条件头透传，304 视为「未变化」
- 404 归类为「无头像」，其他非 2xx 归类为上游故障
- 3xx 不被跟随
- 伪装成图片的 HTML 被拒绝且不写缓存
- `/avatar/{id}` 带 ETag 与 `Cache-Control: private, no-cache`
- `/avatar/me` 返回 `no-store`（该 URL 不区分用户，任何缓存都会串号）

## 不适合交给 extension 的部分

以下仍由 HomeHub 自己做，不要搬：

- **条目入库与去重**：`ON CONFLICT ON link` 依赖数据库，跨进程无法保证
- **保留策略**：`retention_count` 修剪基于入库后的计数
- **图片缓存**：已抽成 `service.RSSImageCache` 接口（默认本地实现，写到
  `web/static/uploads/rss_img`），可通过 `SetImageCache` 替换。远程预下载
  的接入方式与建议契约见 [rss-architecture.md](rss-architecture.md) 第 5 节。

## 待补的能力

按收益排序：

1. **图片处理远程化**：已预留 `RSSImageCache` 接口；下一步在 `/fetch`
   响应中增加 `images` 元数据或新增 `/prefetch` 端点，由 HomeHub 侧实现
   远程 `RSSImageCache` 拉取并登记。建议契约见
   [rss-architecture.md](rss-architecture.md#5-给-homehub-extension-远程预下载的适配)
2. **健康检查**：HomeHub 启动时探测 extension 可达性，配置错误尽早暴露
3. **失败退避**：当前只记 `error_msg`，下一轮按固定间隔重试
4. **结果缓存**：HomeHub 侧不做 HTTP 缓存，每次 `Fetch` 都真发请求

## 不要做的事

- 不要让 extension 复用 HomeHub 的 `session_secret`
- 不要让 extension 直连数据库
- 不要把 fetcher 实现做成有状态的全局单例：并发抓取下必须能被多 goroutine
  同时调用（`autoFetcher` 内部已加锁，自定义实现需自行保证）
