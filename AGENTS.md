# AGENTS.md — HomeHub 工作约定

给在此仓库工作的 AI agent（以及新加入的人）的实用约定。**不是**面向用户的
文档；用户文档见 `README.md` 与 `docs/`。

## 仓库性质

个人自托管项目，**公开仓库**（`github.com/ignvex/HomeHub`）。

- 提交前默认假设「这段内容会被所有人看到」
- 任何凭据、内网地址、私人文件都不得进入版本控制
- 本地环境信息（测试库 DSN 等）放在 `.codewhale/local-env.md`，该目录已在
  `.gitignore` 中。**不要 `git add -f` 它**

## 技术栈

| 层 | 选型 |
| --- | --- |
| 后端 | Go 1.26，Gin，GORM，PostgreSQL |
| 前端 | React 19 + TypeScript 6，Vite 6，Chart.js；无 UI 框架、无 CSS Modules |
| 包管理 | 后端 Go modules；前端 pnpm ≥ 9 |
| 日志 | `log/slog`，控制台 + 数据库 + SSE 广播 |

## 目录职责

```
cmd/homehub/        入口：flag 解析 + app.Run，swag 注解在此
internal/app/       启动编排（配置 → 日志 → 数据库 → 后台任务 → HTTP）
internal/config/    配置加载、校验、热更新
internal/database/  连接池、AutoMigrate 辅助、搜索索引、schema 守卫
internal/handler/   HTTP 处理器（薄层：绑定 → 调 service → 统一响应）
internal/logger/    slog handler（控制台 / 数据库 / SSE 广播）
internal/middleware/ 认证、管理员校验、CSRF、请求日志
internal/model/     GORM 模型
internal/service/   业务逻辑
internal/hubclient/ 调用 Extension 的签名客户端（/hub/*、/fetch、/avatar）
web/src/            前端 SPA
```

分层是硬的：**handler 不写业务逻辑，service 不碰 `*gin.Context`**。
handler 通过包级 `svc *service.Services` 访问业务层，由 `handler.Init` 注入。

## 常用命令

```bash
# 后端
go build ./...
go vet ./...
go test ./...
go test ./internal/service/ -run TestXxx -v

# 前端（在 web/ 下）
pnpm install
pnpm dev        # :5173，代理 /api 与 /static 到 :8080
pnpm build      # tsc -b + vite build
pnpm lint       # oxlint，目标 0 error

# 文档（新增路由后必须重跑）
swag init -g cmd/homehub/main.go --parseDependency --parseInternal -o docs
```

## 测试

- 默认单测用 `sqlmock`（`internal/testutil.NewMockDB`），不需要真实数据库
- 真实 PostgreSQL 用例由 `HOMEHUB_TEST_DB` 启用，未设置时 **自动 skip**
- 集成测试各自建独立 schema，用完 DROP 自己的 schema：
  `it_database`、`it_service`、`it_service_feed`、`it_service_sort`、
  `it_service_state`
- **绝不在 `public` 里建删表**。所有 DDL 必须带 schema 前缀——`search_path`
  里有 `public` 兜底，裸写 `DROP TABLE users` 会一路找到并删掉真表
- 本地 DSN 见 `.codewhale/local-env.md`

写测试的取向（仓库既有风格）：

- 测试名说明**行为**，不说明实现（`TestUpdateFeedWritesNullForClearedGroup`）
- 注释解释「这条断言防的是什么回归」，而不是复述代码
- 能证明「SQL 真的这么发」的，就不要停在「内存对象看起来对」
- 修 bug 先写失败的测试。已有多个文件以此模式存在

## 数据库

- **表名无前缀**（`users`、`rss_feeds`、`log_entries`…）。库被多个项目共用，
  隔离靠 `ddl_guard_test.go` 的显式 allowlist，不靠命名约定
- 新增/改名模型必须同步该 allowlist，否则守卫测试失败
- 破坏性 DDL 不进启动路径，写到 `docs/migrations/` 并在 `CheckSchema` 里
  拒绝启动给出指引
- 字符串截断用 `model.TruncateRunes` / `TruncateEllipsis`，不要用 `s[:n]`：
  按字节切会切碎多字节字符，PostgreSQL 直接拒收整批

## 权限

五级，**数值越小权限越高**，判断是 `user.Level <= required`。

| 级别 | 常量 | 能力 |
| --- | --- | --- |
| 1 | `LevelAdmin` | 完全控制 |
| 2 | `LevelOperator` | 用户管理（不能碰 1 级）、日志 |
| 3 | `LevelEditor` | 全部内容增删改，不分作者 |
| 4 | `LevelMember` | 只能改自己创建的内容 |
| 5 | `LevelViewer` | 只读 |

三条防提权规则必须有：只能创建严格低于自己等级的账号；只能管理严格低于
自己等级的账号；任何人不能改自己的等级或启用状态。`Level=0` 必须 fail
closed（`0 <= 1` 会给出最高权限）。

## 前端

- 无路由库：原生 `<a>` + `history.pushState`，页面映射在 `App.tsx`
- 样式分四层，导入顺序写在 `main.tsx`：令牌 → 重置 → 全局元素 → 组件层 → 页面层
- **组件层 `components.css` 的规则不允许被页面层重复定义**。要改宽高/间距就改
  令牌或加修饰符，不要在页面 CSS 里按下同名类
- 颜色、圆角、间距、字号一律用 `variables.css` 的令牌，不写裸数字
- 组件只做结构与语义，样式统一放 CSS

## 安全底线

- `config.json` 已 gitignore，含数据库密码、API Key、`session_secret`
- 密码 bcrypt；会话走 cookie；CSRF 为 Origin/Referer 校验 + 双提交 cookie
- 媒体资源带 `owner_id`，删除前校验归属
- RSS 正文经清洗后才 `dangerouslySetInnerHTML`
- 抓取外部 URL 时禁止内网地址（SSRF）

## 提交

- Conventional Commits：`feat(scope): …` / `fix(scope): …` / `docs: …` / `chore: …`
- **标题与正文一律用英文。** 仓库是公开的，`git log` 常被非中文读者查阅；
  历史上曾漂到中文，导致同一个仓库两种语言交替。代码、注释、文档正文
  仍用中文，这一条只管提交信息
- 提交信息正文说明**为什么**，不复述 diff
- 一个提交一件事；跨层的大改按可独立构建的切片拆开
- 每个提交都应能单独通过 `go build ./...` 与 `pnpm build`

## 已知的取舍

- SSE 与列表 API 的 `log_time` 必须同为 RFC3339。SSE 曾用手写格式串丢掉时区，
  前端按浏览器时区解析后时刻整体偏移
- RSS 的 `group_id` 用三态表达（缺失 / 显式 null / 具体值）。只用 `*uint64`
  会让「清空分组」与「不改分组」无法区分
- Extension 有两条独立通道：`/fetch`、`/avatar` 由 HomeHub 主动调用
  （`docs/extension.md`）；`/hub/*` 是 Extension 验签的机器通道
  （`docs/hub-integration.md`）。两条都由 `internal/hubclient` 签名发出
  （同一把 Ed25519 密钥），调用方是 `service/extension_hub_service.go`
  （`/hub/*`：订阅镜像、密钥管理）、`service/rss_remote_fetcher.go`
  （`/fetch` 代抓）与 `service/gravatar_source.go`（`/avatar` 头像代取）
