# HomeHub

将多种业务融为一体的个人中枢项目，集成导航页面、RSS 订阅、数据可视化等功能。计划部署在unraid服务的docker上

> 📋 项目详细元数据（技术栈、项目结构、开发约定、排除项、待讨论事项等）见 **[readme.json](readme.json)**。

## 功能模块

### 前端

基于 Vite + React + TypeScript 构建的 SPA（单页应用），Go 后端通过 SPA fallback 提供入口。CSS 变量驱动暗色模式。

#### 功能

- 登录页（单用户密码登录）
- 首页导航栏（搜索框 + 常用书签入口）
- 书签管理（分组、拖拽排序、图标）
- 媒体资源管理（上传图片/视频，管理已上传资源）
- 首页动态背景（单图 / 轮播 / 随机模式，支持淡入淡出/滑动过渡）
- RSS 阅读界面（订阅源管理、条目列表）
- 数据可视化仪表盘（Chart.js）
- 配置管理界面（Web 端编辑 config.json）
- 暗色模式（CSS 变量 + localStorage 持久化）

#### 技术栈

| 层面 | 选型 | 版本 |
|------|------|------|
| 构建工具 | Vite | 6.x |
| UI 框架 | React | 19.x |
| 语言 | TypeScript | 6.x |
| 页面导航 | 原生 &lt;a&gt; + window.location | — |
| 图表 | Chart.js + react-chartjs-2 | 4.x |
| 样式方案 | CSS3 + CSS 变量 + CSS Modules | — |
| 包管理器 | pnpm | 9.x |
| Lint | oxlint | — |

### 后端

- 用户认证（bcrypt + cookie/session）
- 日志系统（`log/slog`）
- RSS 引擎（`gofeed` 定时抓取）
- RESTful API（Gin 框架）
- 多用户管理

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

### 开发工作流

1. 启动 Go 后端：`go run .`（监听 `:8080`）
2. 启动 Vite dev server：`cd web && pnpm dev`（监听 `:5173`）
3. 浏览器访问 `http://localhost:5173`
4. Vite 自动代理 `/api/*` 到 Go 后端，无需手动处理跨域

### 项目结构

```
web/
├── index.html                  ← Vite 入口
├── vite.config.ts              ← 代理 /api→Go:8080，@/ 路径别名
├── tsconfig.json               ← TS 配置
├── package.json                ← 依赖与脚本
├── src/
│   ├── main.tsx                ← 应用入口（导入全局样式）
│   ├── App.tsx                 ← 根组件（路由分发）
│   ├── pages/                  ← 页面目录（每页一个 .tsx 文件）
│   ├── components/             ← 通用 React 组件
│   ├── hooks/                  ← 通用 hooks（useTheme 等）
│   ├── styles/
│   │   ├── variables.css       ← CSS 变量（浅色/暗色主题令牌）
│   │   ├── reset.css           ← 浏览器默认样式重置
│   │   └── global.css          ← 基础布局、卡片、按钮、导航栏
│   └── lib/
│       └── api.ts              ← fetch 封装（get/post/put/delete）
├── dist/                       ← 构建产物（gitignore）
└── old/                        ← 旧前端文件存档（开发参考）
```

### 架构说明

**React SPA 路由分发模式**：

- App.tsx 通过 `window.location.pathname` 解析当前页面路径，从 `pages` 字典映射到对应组件
- 公开页面（`/`、`/login`）直接渲染，认证页面通过 `ProtectedPage` 组件校验登录状态后包裹 `AppShell` 侧边栏布局
- 不使用 React Router 或任何前端路由库，页面导航使用原生 `<a>` 标签
- 暗色模式通过 `useTheme` hook 管理 `html[data-theme]` + localStorage

**开发时前后端协作**：

```
浏览器 :5173 ──▶ Vite Dev Server ──▶ src/* (HMR 热更新)
                    │
                    └── /api/* ──▶ Go :8080 (代理)
```

**生产构建后**：Go 后端通过 `r.Static` 和 `c.File` 直接服务 `web/dist/` 下的构建产物。

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
- PostgreSQL 数据库（已运行，默认连接 `192.168.1.2:5432`）
- 宿主机上准备好 `config.json`（参考项目根目录的 `config.json` 修改数据库连接信息）

### 快速开始

```bash
# 1. 克隆项目
git clone <你的仓库地址> && cd HomeHub

# 2. 编辑 config.json，确保 database 段指向你的 PostgreSQL
#    host: 你的数据库IP
#    password: 你的数据库密码
#    首次启动前填好 auth.password，启动后密码自动清除并写入数据库

# 3. 构建前端（需 Node.js ≥ 20 + pnpm）
cd web && pnpm install && pnpm build && cd ..

# 4. 构建镜像并启动
docker compose build
docker compose up -d

# 5. 验证
curl http://localhost:8080/api/v1/health
# 浏览器访问 http://localhost:8080
```

### 容器端口与挂载

| 端口 | 用途 |
|------|------|
| `8080` | HTTP 服务（API + SPA 前端） |

| 挂载路径 | 用途 | 持久化 |
|----------|------|--------|
| `/app/config.json` | 配置文件（只读） | 是，bind mount |
| `/app/web/static/uploads` | 用户上传的媒体文件 | 是，volume 或 bind mount |

### Unraid 部署要点

1. 安装 **Compose** 插件（Community Applications → 搜索 "compose"）
2. 将项目目录放到 Unraid 共享文件夹，如 `/mnt/user/appdata/homehub/`
3. 修改 `docker-compose.yml` 中的挂载路径：
   ```yaml
   volumes:
     - ./config.json:/app/config.json:ro
     - /mnt/user/appdata/homehub/uploads:/app/web/static/uploads
   ```
4. 若容器无法连接 NAS 上的数据库，在 `docker-compose.yml` 中启用 `network_mode: host`
5. 前端需在开发机上先构建（Unraid 通常不装 Node.js），然后将 `web/dist/` 一并复制到 Unraid：
   ```bash
   # 在开发机上
   cd web && pnpm install && pnpm build
   # 复制整个项目到 Unraid（web/dist/ 会一起过去）
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
| `HOMEHUB_CONFIG` | 配置文件路径 | `/app/config.json` |

## 爬虫模块

### Bilibili

内容生命周期管理系统，通过多入口发现 → 生命周期调度 → TokenBucket 全局限速 → 统一抓取 + 时序快照存储。

#### 架构

```
DiscoveryWorker (每12h)     Scheduler (每分钟)       FetcherWorker (2s/req)
  ──────────────────         ────────────────         ─────────────────
  热门榜 /popular              SELECT WHERE            Take token → GET API
  排行榜 /ranking/v2           next_update_at          → upsert 主表
  动态流 /dynamic/feed         <= NOW()                → 条件写入快照
   UP 主动态 (预留)            ORDER BY priority       → 计算生命周期
       │                        │                      → UPDATE next_update_at
       ▼                        ▼                              │
  BloomFilter 去重           task channel 入队                │
       │                        │                              │
       └────────────────────────┼──────────────────────────────┘
                                ▼
                         Database
                    ┌──────────────────┐
                    │ videos/articles  │  ← 最新状态
                    │ /bili_users      │     (lifecycle_stage,
                    │                  │      next_update_at)
                    ├──────────────────┤
                    │ *_stats_snapshots│  ← 时序快照
                    │                  │     (≤30 条/实体,
                    │                  │      折线压缩清理)
                    └──────────────────┘
```

- **DiscoveryWorker**：每 12h 从热门榜/分区排行榜等入口发现新视频，经 Bloom Filter 去重后写入 DB 占位（lifecycle=newborn），同时触发关联 UP 主发现
- **Scheduler**：每分钟扫描 next_update_at 到期的任务，按 priority_score 排序入队
- **FetcherWorker**：从 task channel 取任务 → GlobalTokenBucket（全局限速）→ 调用 B 站 API → UPSERT 主表 → 条件写入快照 → 计算生命周期阶段 → 更新 next_update_at 和 priority_score
- **生命周期**：newborn (<2h, 30min更新) → growing (<24h, 2h) → mature (<7d, 12h) → archived (7d)
- **时序快照**：video/article/user 三类统计按时间存为独立快照行，支持播放量增长曲线查询；通过折线简化算法自动清理冗余快照（≤30 条/实体）
- **反爬防护**：GlobalTokenBucket 全局统一限速（2s/req）避免三爬虫扎堆；25 条多平台多设备 UA 池轮换；独立代理支持；指数退避 + 全抖动重试策略

#### 配置

完整配置见 `config.json` → `bilibili` 段，包含开关、认证、代理、UA 池、速率、生命周期、发现策略、快照策略。开关以 config.json 为唯一来源。

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/bilibili/stats` | 存储空间预估 + 调度进度 |
| GET | `/bilibili/crawlers` | 爬虫运行状态（enabled 从 config 读取） |
| GET | `/bilibili/stats/lifecycle` | 生命周期阶段分布（newborn/growing/mature/archived） |
| GET | `/bilibili/videos/:aid/snapshots` | 视频统计快照时序 |
| GET | `/bilibili/articles/:cvid/snapshots` | 专栏统计快照时序 |
| POST | `/bilibili/fetch/video/:aid` | 按 AID 即时抓取 |
| POST | `/bilibili/fetch/article/:cvid` | 按 CVID 即时抓取 |

详细 API 文档见 `docs/swagger.yaml`。
