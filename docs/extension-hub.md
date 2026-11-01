# Extension hub 通道

> **实验性。** 对端 HomeHub Extension 仍处于 alpha，其 API 契约可能在 1.0
> 正式版前微调。本通道默认关闭，建议先在联调环境跑通再上生产。

本文说明 HomeHub **作为宿主**如何接入 HomeHub Extension 的 `/api/v1/hub/*`
签名通道。协议本身（待签消息、错误码、端点语义）以
[hub-integration.md](hub-integration.md) 为准，这里只讲 HomeHub 这一侧的实现取舍。

## 1. 与 `rss.extension_url` 的区别

两个配置项都指向 Extension，但做的是完全不同的事，不要混用：

| | `rss.extension_url` | `extension_hub` |
| --- | --- | --- |
| 方向 | HomeHub 主动 GET `/fetch?url=…` | HomeHub 主动签名调用 `/api/v1/hub/*` |
| 认证 | Bearer（`extension_token`） | Ed25519 请求签名 |
| 数据归属 | 条目仍落在 HomeHub 的 PostgreSQL | 订阅与条目落在 Extension 的 SQLite + blob |
| 作用 | 把抓取外包出去，阅读仍在 HomeHub | 让 Extension 拥有一份独立的阅读器与登录体系 |

可以同时开启，但那样同一批 feed 会被抓两遍。只在确实需要
「手机上用 Extension 读、内网用 HomeHub 读」时才两个都开。

## 2. 配置

```json
{
  "extension_hub": {
    "enabled": true,
    "base_url": "https://ext.example.com/api/v1",
    "private_key_path": "/etc/homehub/extension-hub.key",
    "timeout_seconds": 30
  }
}
```

`base_url` **必须**带 `/api/v1`。少了前缀时所有请求会打到 `/hub/*` 之外的
路径，表现为一律 404——而 404 在协议里恰好又表示「RSS 模块未启用」，两个
问题撞在同一个状态码上极难排查，所以配置校验直接拒绝。

### 2.1 生成并部署密钥

```bash
openssl genpkey -algorithm ed25519 -out extension-hub.key
openssl pkey -in extension-hub.key -pubout -out extension-hub.pub
chmod 600 extension-hub.key
```

私钥留在 HomeHub 主机上；公钥放到 Extension 主机的
`auth.hub.public_key_path`（默认 `/etc/hhe/homehub.pub`）并重启 Extension。

私钥只在本进程内读取：不写日志、不入库、不下发前端。`enabled` 与两个路径
任一变化时客户端会重建；私钥**内容**变化也会重读——客户端记下文件的
`mtime` 与大小，所以直接用同名覆盖文件完成轮换即可，不需要重启。

> 校验发生在每次调用前，不是进程启动时一次。这与 Extension 侧「公钥启动时
> 读一次」不同：那边漏读的代价是重启一次，这边漏读的代价是一直签出 401
> 而没有任何提示指向「该重读私钥」。

## 3. HomeHub 暴露的端点

| 方法 | 路径 | 最低权限 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/extension/status` | 登录 | 四层状态探测，不抛错，结果在 `data` 里 |
| `GET` | `/api/v1/extension/subscriptions` | 登录 | Extension 侧订阅，标注本地是否也有 |
| `POST` | `/api/v1/extension/subscriptions` | 3 级 | 推送单条 `{feed_id, enabled?, fetch_now?}` |
| `POST` | `/api/v1/extension/subscriptions/sync` | 3 级 | 全量对账，只推不删 |
| `DELETE` | `/api/v1/extension/subscriptions/{id}` | 2 级 | 删除远端订阅，不可恢复 |
| `GET` | `/api/v1/extension/keys` | 2 级 | 访问密钥列表 |
| `POST` | `/api/v1/extension/keys` | 2 级 | 签发，明文只出现一次 |
| `DELETE` | `/api/v1/extension/keys/{id}` | 2 级 | 撤销，连带失效其会话 |

密钥相关端点抬到 2 级、删除远端订阅也抬到 2 级：前者等同于发账号，后者
会连带清掉 Extension 已抓取的条目与 blob。两者都不属于「内容写」。

## 4. 三处刻意的语义

### 4.1 省略就是保留，所以映射要传 nil

Extension 用「字段省略」表达「保留原值」。因此推送时：

- 本地标题为空 → **不发** `title`（发空串会把对方抓到的标题清掉）
- 本地间隔为 0 → **不发** `interval`（发 0 会被夹到下限，值合法但语义错）
- 例行对账 → **不发** `enabled`（发 true 会把用户手动暂停的源重新打开）

`fetch_now` 相反，必须**总是显式发送**：协议里省略等于 `true`，不显式传
`false` 就表达不出「只更新、别抓」。对账时全部传 `false`，否则一次同步会对
N 个源同时发起抓取。

### 4.2 对账只推不删

`POST /extension/subscriptions/sync` 把本地订阅补到 Extension，但**不会**删除
Extension 上本地没有的条目。删除会连带清掉对方已抓取的条目与 blob，不可
恢复；把它混在一次「对账」里悄悄做掉是最不该发生的事。要删就逐条走
`DELETE`，界面上有单独的确认框。

### 4.3 上游状态码不透传

Extension 的 401 表示「宿主签名不对」，与 HomeHub 的会话无关。原样透传会
让前端把用户踢回登录页——把一次部署配置错误演成「你掉登录了」。同理，
上游的 404 也不代表 HomeHub 里没有这个资源。

因此所有上游错误统一折成：

| HomeHub 状态 | `code` | 含义 |
| --- | --- | --- |
| 503 | `70001` | 对接未启用（`extension_hub.enabled` 为 false） |
| 502 | `70002` | 上游不可达、签名失败、模块关闭、上游 4xx/5xx；以及启用后配置不齐 |
| 500 | `10007` | 本地数据库读写失败（不是上游的问题） |

启用后漏填 `base_url` / `private_key_path` 归入 502 而不是 70001：开关
已经打开，该改的是具体字段而不是那个开关本身。本地存储失败单列 500——
把它报成「上游不可达」会把人送去查另一台机器。

原始协议错误码与 `request_id` 保留在 `message` 里，报障时带上它就能和
Extension 的 JSON 日志对上。

## 5. 状态探测的四个层次

`GET /extension/status` 一次回答四个独立的问题，因为它们对应四种不同的修法：

| 字段 | 为真说明 | 为假时该查什么 |
| --- | --- | --- |
| `configured` | 配置齐全且私钥可读 | `config.json` 的 `extension_hub` 段与键文件路径 |
| `reachable` | HTTP 层通了 | 地址、网络、Extension 是否在跑 |
| `authenticated` | 签名被接受 | 时钟偏差、两边公钥是否配对 |
| `rss_available` | 对方 RSS 模块开着 | Extension 的 `rss.enabled` |

探测顺序是先 `/hub/keys` 再 `/hub/subscriptions`，因为前者不受 `rss.enabled`
影响。两个端点都会对不存在的路径返回 404，只有先拿 keys 验签，才能把
「签名通过但模块关着」与「base_url 写错了」分开——这两种情况的修法完全不同。
`TestStatusTreatsMissingKeysEndpointAsWrongBaseURL` 锁住了这个区分。

`public_key` 字段返回宿主公钥（标准 base64），部署时直接和 Extension 侧
配置的公钥比对，不用去 `openssl` 里翻。

## 6. 排障

先看 `/extension/status` 的 `error_code`，再按状态分层：

- `disabled` / `config` —— 没启用或配置不齐，改 `config.json`
- `network` —— 连不上，查地址与容器网络
- `unauthenticated` —— 时钟偏差超过 `clock_skew`（默认 60 秒），或公钥不配对
- `unavailable` —— Extension 起来了但签名通道不可用，通常是公钥文件缺失或
  解析失败；它日志里会有一条 `hub signature channel disabled`
- `not_found` —— 签名通过但 RSS 模块关闭

`request_id` 是唯一能和 Extension 日志对上的线索，任何上游错误都会带上它。
