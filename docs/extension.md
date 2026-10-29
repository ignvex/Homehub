# HomeHub Extension 接入

`homehub-extension` 是独立部署的抓取加速服务。本文定义它与 HomeHub 的
通信契约。

## 通信方向：单向，HomeHub 发起

**HomeHub 是唯一发起方。** extension 不连接 HomeHub、不持有数据库凭据、
不推送数据。

```
HomeHub ──GET {extension_url}/fetch?url=...──▶ homehub-extension
        ◀──── 原始 RSS/Atom XML 或 JSON ─────
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
GET {extension_url}/fetch?url=<URL 编码的订阅源地址>
Accept: application/xml, application/rss+xml, application/atom+xml, application/json
Authorization: Bearer <extension_token>        # token 非空时携带
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

`config.json` → `rss`：

| 字段 | 说明 |
|------|------|
| `extension_url` | 为空时本地抓取（默认）；非空时全部交给 extension |
| `extension_token` | 调用 extension 的 Bearer Token，留空则不带认证头 |

```json
"rss": {
  "extension_url": "http://192.168.1.2:8090",
  "extension_token": "change-me",
  "fetch_timeout_seconds": 30
}
```

地址必须是完整的 `http(s)://host[:port]`，否则配置校验不通过。
改完即生效（下一次抓取就会用新地址），**无需重启**：
`autoFetcher` 按配置指纹重建底层实现。

## 实现示例

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

- 请求确实是 `GET /fetch?url=`，且带 `Authorization: Bearer`
- XML、`{"feed": ...}`、gofeed JSON 三种响应都能解析
- 非 2xx 返回带状态码的错误
- 配置在 `local` / `remote` 之间切换即时生效

本地联调时把 `extension_url` 指向 `httptest` 服务器或临时进程即可。

## 不适合交给 extension 的部分

以下仍由 HomeHub 自己做，不要搬：

- **条目入库与去重**：`ON CONFLICT ON link` 依赖数据库，跨进程无法保证
- **保留策略**：`retention_count` 修剪基于入库后的计数
- **图片缓存**：目前固定在 HomeHub 进程内（写到 `web/static/uploads/rss_img`），
  没有抽接口。若将来要远程化，需要扩展契约返回图片而非条目

## 待补的能力

按收益排序：

1. **图片处理远程化**：新增 `ImageCache` 接口，让 extension 批量取图
2. **健康检查**：HomeHub 启动时探测 extension 可达性，配置错误尽早暴露
3. **失败退避**：当前只记 `error_msg`，下一轮按固定间隔重试
4. **结果缓存**：HomeHub 侧不做 HTTP 缓存，每次 `Fetch` 都真发请求

## 不要做的事

- 不要让 extension 复用 HomeHub 的 `session_secret`
- 不要让 extension 直连数据库
- 不要把 fetcher 实现做成有状态的全局单例：并发抓取下必须能被多 goroutine
  同时调用（`autoFetcher` 内部已加锁，自定义实现需自行保证）
