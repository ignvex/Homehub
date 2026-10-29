# HomeHub

将多种业务融为一体的个人中枢项目，集成导航页面、书签、RSS 订阅、待办、剪贴板与 LLM 余额监控等功能。部署在 Unraid 的 Docker 上。

文档入口：

| 文档 | 内容 |
|------|------|
| [docs/features.md](docs/features.md) | 各功能模块的行为、配置、接口与已知限制 |
| [docs/extension.md](docs/extension.md) | 外部抓取加速服务的通信契约 |
| [docs/swagger.yaml](docs/swagger.yaml) | API 请求/响应结构（运行时 `/swagger/index.html`） |

> swagger 由 `swag init -g cmd/homehub/main.go --parseDependency --parseInternal -o docs` 生成，新增路由后需重新生成。

## 功能模块

### 前端

基于 Vite + React + TypeScript 构建的 SPA（单页应用），Go 后端通过 SPA fallback 提供入口。CSS 变量驱动暗色模式，不使用前端路由库。

#### 功能

- 登录页（多用户密码登录，首次由 `config.json` 创建管理员）
- 仪表盘（服务健康、数据库连接、运行时长、余额概览）
- 首页导航栏（搜索框 + 常用书签入口）
- 书签管理（分组、上下移动排序、图标）
- 媒体资源管理（上传图片/视频/音频/PDF，管理已上传资源）
- 动态背景（首页单图 / 轮播 / 随机 + 淡入淡出/滑动过渡；登录页支持单图 / 轮播 / 随机）
- RSS 阅读（订阅源、分组、条目列表、并发后台抓取）
- 待办事项（多用户共享、排序、状态切换）
- 全局搜索（书签、RSS 条目）
- LLM 余额监控（Chart.js 饼图）
- 剪贴板（公开 API，按 TTL 自动清理）
- 系统日志（SSE 实时推送，管理员）
- 配置管理（常用配置域，仅管理员）
- 用户管理（管理员）
- 个人中心（改用户名、邮箱、密码；头像随邮箱 Gravatar 重新加载）
- 暗色模式（CSS 变量 + localStorage 持久化）

#### 技术栈

| 层面 | 选型 | 版本 |
|------|------|------|
| 构建工具 | Vite | 6.x |
| UI 框架 | React | 19.x |
| 语言 | TypeScript | 6.x |
| 客户端导航 | 原生 `<a>` + `history.pushState` | — |
| 图表 | Chart.js + react-chartjs-2 | 4.x / 5.x |
| 样式方案 | CSS3 + CSS 变量（无 UI 框架、无 CSS Modules） | — |
| 包管理器 | pnpm | ≥ 9（lockfile 9.0） |
| Lint | oxlint | 1.x |

### 后端

- 用户认证（bcrypt + cookie/session，多用户与 admin/user 角色）
- 日志系统（`log/slog`，控制台 + 数据库 + SSE 广播）
- RSS 引擎（`gofeed` 定时抓取，图片本地缓存）
- 剪贴板、待办、余额监控、全局搜索
- RESTful API（Gin 框架）
- PostgreSQL（GORM AutoMigrate，启动时自动建表与索引）

## 前端开发环境

### 前置条件

- Node.js ≥ 20
- pnpm ≥ 9（`corepack enable && corepack prepare pnpm@latest --activate`）

### 快速开始

```bash
cd web
pnpm install      # 安装依赖
pnpm dev          # 启动开发服务器 → http://localhost:5173
pnpm build        # 生产构建 → dist/
```

> pnpm 10+ 首次安装若提示 `Ignored build scripts: esbuild`，执行 `pnpm approve-builds` 并勾选 esbuild，然后重新 `pnpm install`。

### 开发工作流

1. 准备配置：`cp config.example.json config.json` 并填写数据库与凭据
2. 启动 Go 后端：`go run ./cmd/homehub`（监听 `:8080`）
3. 启动 Vite dev server：`cd web && pnpm dev`（监听 `:5173`）
4. 浏览器访问 `http://localhost:5173`
5. Vite 自动代理 `/api` 和 `/static` 到 Go 后端，无需手动处理跨域

### 项目结构

```
cmd/
└── homehub/
    └── main.go                 ← 入口：flag 解析 + app.Run，swag 注解在此
internal/
├── app/                        ← 启动编排（配置 → 日志 → 数据库 → 后台任务 → HTTP）
├── config/                     ← 配置加载、校验、热更新
├── database/                   ← 连接池、AutoMigrate 辅助、搜索索引
├── handler/                    ← HTTP 处理器（薄层，调用 service）
├── logger/                     ← slog handler（控制台 / 数据库 / SSE 广播）
├── middleware/                 ← 认证、管理员校验、请求日志
├── model/                      ← GORM 模型
├── server/                     ← 路由注册与优雅关闭
└── service/                    ← 业务逻辑（RSS、书签、待办、余额…）

web/
├── index.html                  ← Vite 入口
├── vite.config.ts              ← 代理 /api、/static → Go:8080，@/ 路径别名
├── tsconfig.json               ← TS 配置
├── package.json                ← 依赖与脚本
├── src/
│   ├── main.tsx                ← 应用入口（导入全局样式）
│   ├── App.tsx                 ← 认证缓存 + 客户端导航 + 页面映射
│   ├── pages/                  ← 页面目录（每页一个 .tsx）
│   ├── components/             ← AppShell、ThemeToggle、UserAvatar、WorkspaceUI
│   ├── hooks/                  ← 通用 hooks（useTheme 等）
│   ├── styles/
│   │   ├── variables.css       ← CSS 变量（浅色/暗色主题令牌）
│   │   ├── reset.css           ← 浏览器默认样式重置
│   │   └── global.css          ← 基础布局、卡片、按钮、导航栏
│   └── lib/
│       └── api.ts              ← fetch 封装（get/post/put/delete）
├── static/                     ← 静态资源；uploads/ 为运行时上传与图片缓存目录
└── dist/                       ← 构建产物（gitignore）
```

### 架构说明

**SPA 路由与认证**：

- `App.tsx` 用 `pages` 字典映射路径，未命中时渲染 404
- 公开页面：`/`、`/login`、`/paste`
- 其余页面先请求 `/api/v1/auth/status`；未登录时跳转 `/login?returnTo=...`
- 标记为 `admin: true` 的页面（用户、日志、设置）会校验 `auth.role`
- 不使用 React Router：拦截同源 `<a>` 点击后调用 `history.pushState`，并监听 `popstate` 处理前进/后退
- 暗色模式通过 `useTheme` hook 管理 `html[data-theme]` + localStorage

**开发时前后端协作**：

```
浏览器 :5173 ──▶ Vite Dev Server ──▶ src/* (HMR 热更新)
                    │
                    ├── /api/*    ──▶ Go :8080 (代理)
                    └── /static/* ──▶ Go :8080 (代理)
```

**生产构建后**：Go 后端通过 `r.Static("/static", "./web/static")` 提供静态资源；未匹配的 GET 请求回退到 `web/dist/index.html`，带扩展名的请求返回 `web/dist` 下的对应文件。

### 常用命令

| 命令 | 说明 |
|------|------|
| `pnpm dev` | 启动 Vite 开发服务器（:5173） |
| `pnpm build` | TypeScript 检查 + Vite 生产构建 |
| `pnpm preview` | 预览生产构建产物 |
| `pnpm lint` | 运行 oxlint 代码检查 |

## Docker 部署

项目通过 Docker 容器部署，推荐使用 docker-compose 一键启动。

### 前置条件

- Docker + Compose 插件
- PostgreSQL 数据库（已运行）
- 项目根目录下有 `config.json`（从 `config.example.json` 复制并修改）

> `config.json` 含数据库密码与 `session_secret` 等凭据，已被 `.gitignore` 排除，不会被提交。

### 快速开始

```bash
# 1. 克隆项目
git clone <你的仓库地址> HomeHub && cd HomeHub

# 2. 准备配置
cp config.example.json config.json
# 编辑 config.json：
#   database.*            数据库连接（host / port / user / password / dbname）
#   auth.password         初始管理员密码（首次启动后自动清空）
#   auth.session_secret   至少 32 字符，可用 openssl rand -hex 32 生成
#   balance_monitor.targets[].api_key  必填（每个 target 都要填，与 enabled 无关）

# 3. 构建前端（需 Node.js ≥ 20 + pnpm）
cd web && pnpm install && pnpm build && cd ..

# 4. 交叉编译后端（Dockerfile 只打包，不在容器内编译）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o homehub ./cmd/homehub
# ARM NAS 请改用 GOARCH=arm64

# 5. 构建镜像并启动
docker compose build
docker compose up -d

# 6. 验证
curl http://localhost:8080/api/v1/health
# 浏览器访问 http://localhost:8080
```

> `config.json` 必须可写：首次创建管理员后程序会清空 `auth.password`，Web 端配置更新也会写回该文件。

### 容器端口与挂载

| 端口 | 用途 |
|------|------|
| `8080` | HTTP 服务（API + SPA 前端 + `/static`） |

| 挂载路径 | 用途 | 要求 |
|----------|------|------|
| `/app/config.json` | 配置文件 | 必须挂载，可写 |
| `/app/web/static/uploads` | 上传文件与 RSS 图片缓存 | 持久化（volume 或 bind mount） |

> `config.json` 是单文件 bind mount。若宿主机上该文件不存在，Docker 会创建同名**目录**，导致启动失败——务必先 `cp config.example.json config.json`。

### Unraid 部署要点

1. 安装 **Compose** 插件（Community Applications → 搜索 "compose"）
2. 将项目目录放到 Unraid 共享文件夹，如 `/mnt/user/appdata/homehub/`
3. 修改 `docker-compose.yml` 中的挂载路径：
   ```yaml
   volumes:
     - ./config.json:/app/config.json
     - /mnt/user/appdata/homehub/uploads:/app/web/static/uploads
   ```
4. 若容器无法连接 NAS 上的数据库，在 `docker-compose.yml` 中启用 `network_mode: host`
5. Unraid 通常不装 Node.js/Go：在开发机上构建 `web/dist` 和 `homehub` 二进制，再复制整个项目：
   ```bash
   # 在开发机上
   cd web && pnpm install && pnpm build && cd ..
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o homehub ./cmd/homehub
   # 复制整个项目到 Unraid（web/dist/ 与 homehub 会一起过去）
   scp -r ../HomeHub root@unraid:/mnt/user/appdata/homehub/
   ```
6. Unraid 终端执行：
   ```bash
   cd /mnt/user/appdata/homehub
   docker compose build
   docker compose up -d
   ```

### 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `HOMEHUB_CONFIG` | 配置文件路径 | `config.json`（容器内工作目录为 `/app`） |

> Web 端配置更新（含背景设置）与首次启动的密码清除都会写回 `HOMEHUB_CONFIG` 指向的文件。

### 数据库结构

没有需要手工执行的 SQL。结构变更全部在启动时自动完成，幂等可重复运行：

| 阶段 | 内容 | 实现 |
|------|------|------|
| 1 | 建表、加列、普通索引 | `db.AutoMigrate(...)`，模型 tag 驱动 |
| 2 | `homehub_pastes` 表 | `model.CreatePastesTable`，带建表选项的表单独处理 |
| 3 | `pg_trgm` 扩展 + 模糊搜索 GIN 索引 | `database.EnsureSearchIndexes`，失败仅告警 |

第 3 步是纯性能优化：账号没有 `CREATE EXTENSION` 权限时只记录警告，服务照常启动，搜索退化为全表扫描。

> 旧版的 `migrations/*.sql` 已全部移除：列变更由 AutoMigrate 覆盖，表重命名只服务于历史升级，`DROP TABLE` 属于破坏性操作不应出现在启动流程。

**本仓库不会创建也不会删除 Bilibili 的表。** `bilibili_*` 与 `crawler_state` 等表由独立的 B 站爬虫项目维护；同一个库可能同时被两个项目使用，双方各管各的表。

> ⚠️ 库里可能还留一张 `homehub_crawler_state`：它是旧版重命名脚本把 `crawler_state` 改名后的结果，HomeHub 已不再使用。**不要随手删它** —— 那个爬虫项目可能已经适配了新表名。先确认它在读哪一张表，再决定是否清理。

`internal/database/ddl_guard_test.go` 把这条约束变成了测试：代码里出现 `DROP TABLE` / `TRUNCATE` / `ALTER TABLE`，或者任何 DDL 指向非 `homehub_` 前缀的表，测试就会失败。

### 升级提示

从包含爬虫的旧版本拉取本次提交后：

- `config.json` 已从版本库移除，`git pull` 会删掉工作区里的该文件。拉取前先备份，拉取后 `cp config.example.json config.json` 再把原值填回去
- `web/static/uploads/` 下的 RSS 图片缓存已不再跟踪。本机已有文件不受影响；但如果是从远端新克隆，历史 RSS 条目里内嵌的 `/static/uploads/rss_img/...` 图片会 404（重新抓取只更新 `magnet_url`，不会重建 content），新条目会自动重新缓存
- 旧库如果还是无前缀表名（`users` / `bookmarks` / `rss_entries` 等），重命名脚本已移除。要么手工 `ALTER TABLE ... RENAME TO homehub_*`，要么直接建新库让 AutoMigrate 重建 —— 后者会丢掉旧数据

### 安全提示

- `config.json` 已加入 `.gitignore`；上传目录与 RSS 图片缓存属于运行时数据，也不再纳入版本库
- **历史提交 `5db5f05` 已包含当时真实的 `config.json`（数据库密码、`session_secret`、B 站 Cookie、API Key）与上传图片，并已保存在 `origin/main`。**清理工作区只能阻止新增泄露，不能从已有历史中抹除。请立即轮换上述凭据；如果要把历史一起清理，需用 `git filter-repo` 重写并强制推送（会改变所有提交 hash，需仓库协作者配合）

## RSS 抓取调优

抓取是 HomeHub 里最容易变慢的环节：源站响应慢、超时、单条目含几十张图。
当前实现在调度层做了三件事：

- **并发抓取**：`fetch_concurrency`（默认 4）控制同时抓取的订阅源数，
  单个源超时只占一个槽位，不会像串行实现那样把整轮堵死
- **独立超时**：`fetch_timeout_seconds`（默认 30）是每个订阅源的预算，
  超时只影响该源，其他源继续
- **图片并发缓存**：单条目内的图片按 `image_cache_concurrency`（默认 6）
  并发下载，并受该源的超时预算约束

配置项都在 `config.json` → `rss` 段，热更新即时生效（代理与 UA 变更
会在下次抓取时重建 HTTP 客户端，无需重启）：

| 配置 | 默认 | 说明 |
|------|------|------|
| `fetch_concurrency` | 4 | 同时抓取的订阅源数 |
| `fetch_timeout_seconds` | 30 | 单源抓取预算（秒） |
| `image_cache_enabled` | true | 关闭后图片保留外链，抓取明显更快、不占磁盘 |
| `image_cache_concurrency` | 6 | 单条目图片并发下载数 |
| `background_fetch_interval` | 300 | 后台轮询间隔（秒），改动 5 秒内生效 |

如果源站图片本身可直连，把 `image_cache_enabled` 设为 `false` 是最省事的提速手段。

### 交给外部服务抓取

抓取实现被抽成了 `service.RSSFetcher` 接口，可通过 `SetFetcher` 替换。
计划中的 `homehub-extension` 项目（独立进程/容器，集中做并发、缓存与去重）
就是从这里接入的。完整契约、已就绪部分与待补空缺见
[docs/extension.md](docs/extension.md)。

## 仓库维护

- `config.example.json` 是唯一可提交的配置模板；`config.json` 已被 `.gitignore` 排除
- `web/static/uploads/` 只保留 `.gitkeep`，上传文件与 RSS 图片缓存不提交
- `*.lnk`、`*.log`、`.env`、`.codewhale/` 及 CodeWhale 元数据（`readme.json` 等）均已忽略
- 提交前可用 `git status --short` 确认没有运行时数据或凭据进入版本库
- 修改 handler 的路由注释后，用 `swag init -g cmd/homehub/main.go --parseDependency --parseInternal -o docs` 重新生成 API 文档
