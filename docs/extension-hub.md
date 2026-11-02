# Extension hub 通道

> **实验性。** 对端 HomeHub Extension 仍处于 alpha，其 API 契约可能在 1.0
> 正式版前微调。本通道默认关闭，建议先在联调环境跑通再上生产。

本文说明 HomeHub **作为宿主**如何接入 HomeHub Extension 的 `/api/v1/hub/*`
签名通道。协议本身（待签消息、错误码、端点语义）以
[hub-integration.md](hub-integration.md) 为准，这里只讲 HomeHub 这一侧的实现取舍。

## 1. 一个连接，三种能力

Extension 只由 `extension` 一段配置描述，认证只有 Ed25519 签名一种。
能力分开声明，因为对端不一定都实现了：

| 能力 | 开关 | 端点 | 认证 |
| --- | --- | --- | --- |
| 订阅镜像与访问密钥 | `extension.features.hub` | `/api/v1/hub/*` | Ed25519 签名 |
| RSS 抓取代理 | `extension.features.fetch` | `/api/v1/fetch?url=` | Ed25519 签名 |
| 头像代理 | `extension.features.avatar` | `/api/v1/avatar/{hash}` | Ed25519 签名 |

**为什么能力要分开**：三条通路共用一份地址与一把密钥，但 Extension 的
实现进度不一样。用一个开关同时控制，打开签名通道的瞬间会让还没实现的
`/fetch`、`/avatar` 一起 404——而那两条通路本来工作得好好的。缺省全部
关闭，升级后行为与升级前一致。

`features.hub` 与 `fetch`/`avatar` 的语义也不同：前者是「让 Extension 拥有
独立的阅读器」，条目落在对端；后两者是「只把出网这一步外包」，条目仍然
落在 HomeHub 的 PostgreSQL。可以只开前者、只开后两者，或全开。

## 2. 配置

```json
{
  "extension": {
    "enabled": true,
    "base_url": "https://ext.example.com/api/v1",
    "private_key_pem": "<44 字符 base64 种子>",
    "features": { "hub": true, "fetch": false, "avatar": false },
    "proxy_names": [],
    "timeout_seconds": 30
  }
}
```

`base_url` **必须**带 `/api/v1`。少了前缀时所有请求会打到 `/hub/*` 之外的
路径，表现为一律 404——而 404 在协议里恰好又表示「RSS 模块未启用」，两个
问题撞在同一个状态码上极难排查，所以配置校验直接拒绝。

`proxy_names` **默认空 = 直连**。Extension 通常在内网或同机部署，绕一层
外部代理反而更慢，很多代理也不转发内网地址。这与全局 `proxy.default`
是两个独立的东西。

`private_key_pem` 建议写成**一行 base64 种子**（44 字符）。PEM 是四行文本，
进 JSON 要转义换行，粘进 `config.json` 后既难读也容易在复制过程中混入
不可见字符。三种形态都接受：

| 形态 | 长度 | 来源 |
| --- | --- | --- |
| `base64(32 字节种子)` | 44 | 设置页保存时规范成这样，推荐 |
| `base64(64 字节私钥)` | 88 | `openssl` 导出私钥后直接 base64 |
| PKCS#8 PEM | 多行 | 直接粘贴 `openssl genpkey` 的输出 |

种子足以还原完整私钥（`ed25519.NewKeyFromSeed`），不损失任何能力。

私钥写在这里而不是单独的文件或数据库：`config.json` 本身已是最高敏感度
的文件（0600、gitignore、备份清单必有它），再拆一处只会增加运维负担。
程序在加载时会把它收紧到 0600。Web 端设置页可以粘贴写入，但读取接口
永不返回私钥内容。

### 2.1 生成并部署密钥

推荐用一条命令直接产出可粘贴的两串（各自一行）：

```bash
# 私钥（44 字符 base64 种子）与公钥（PKIX PEM）
openssl genpkey -algorithm ed25519 -out /tmp/hub.key
openssl pkey -in /tmp/hub.key -outform DER | tail -c 32 | base64 -w0; echo
openssl pkey -in /tmp/hub.key -pubout
rm -f /tmp/hub.key
```

第一行输出粘进 `extension.private_key_pem`，第二段粘到 Extension 主机的
`auth.hub.public_key_path`（默认 `/etc/hhe/homehub.pub`）并重启 Extension。

也可以在设置页直接粘贴 `openssl genpkey` 产生的 PEM：保存时会自动规范成
单行 base64，之后 `config.json` 里就只剩那 44 个字符。

私钥只在本进程内读取：不写日志、不入库、不下发前端。客户端按
`base_url` + 私钥**内容指纹**缓存，任一变化即重建——所以轮换就是改配置
里那一行，热重载后立即生效，不需要重启服务。

轮换时记住同步更新 Extension 侧的公钥，否则新私钥签出的请求会一律 401。

> 校验发生在每次调用前，不是进程启动时一次。这与 Extension 侧「公钥启动时
> 读一次」不同：那边漏读的代价是重启一次，这边漏读的代价是一直签出 401
> 而没有任何提示指向「该重读私钥」。

## 3. HomeHub 暴露的端点

| 方法 | 路径 | 最低权限 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/extension/status` | 登录 | 四层状态探测，不抛错，结果在 `data` 里 |
| `GET` | `/api/v1/extension/subscriptions` | 登录 | Extension 侧订阅，标注本地是否也有 |
| `POST` | `/api/v1/extension/subscriptions` | 3 级 | 推送单条 `{feed_id, enabled?, fetch_now?}` |
| `POST` | `/api/v1/extension/subscriptions/sync` | 3 级 | 对账 `fetch_by=extension` 的源，只推不删 |
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

### 4.3 对账只推 `fetch_by=extension` 的源

`fetch_by` 是**阅读器归属**，不是抓取开关（见 `model.RSSFeed` 的注释）。
归属本机的源推给 Extension 会得到两份各不相通的条目：Extension 新建订阅
默认启用，于是同一个源两边各抓一次，而使用者只会在其中一处阅读。

所以对账只推 `fetch_by=extension`，判断复用 `model.NormalizeFetchBy`——
与 `RSSService.FetchAll` 同一个函数，两处必须同源。写反任何一边都会造出
「本机不抓、也没推给对方」的源，静默停摆。

响应里的 `total` 只算实际推送的条数，被跳过的本机源单独报在
`skipped_local`。不分开的话，`total=0` 会同时表示「本地没订阅源」和
「有源但全归本机」两件完全不同的事。

**已知不对等**：把某个源从 `extension` 改回 `local` 之后，Extension 侧那条
仍然存在并继续抓取。对账不能自动删（见 §4.2），需要用户到 Extension 界面
手动删掉。

### 4.4 上游状态码不透传

Extension 的 401 表示「宿主签名不对」，与 HomeHub 的会话无关。原样透传会
让前端把用户踢回登录页——把一次部署配置错误演成「你掉登录了」。同理，
上游的 404 也不代表 HomeHub 里没有这个资源。

因此所有上游错误统一折成：

| HomeHub 状态 | `code` | 含义 |
| --- | --- | --- |
| 503 | `70001` | 对接未启用（`extension.enabled` 或 `extension.features.hub` 为 false） |
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
| `configured` | 配置齐全且私钥可读 | `config.json` 的 `extension` 段 |
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
