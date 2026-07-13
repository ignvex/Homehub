# HomeHub

将多种业务融为一体的个人中枢项目，集成导航页面、RSS 订阅、数据可视化等功能。计划部署在unraid服务的docker上

> 📋 项目详细元数据（技术栈、项目结构、开发约定、排除项、待讨论事项等）见 **[readme.json](readme.json)**。

## 功能模块

### 前端

基于 Vite + React + HTMX 的"群岛架构"：HTMX 负责页面导航与表单提交，React 嵌入复杂交互模块（图表、轮播），CSS 变量驱动暗色模式。

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
| 页面导航 | HTMX | 2.x |
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
├── index.html                  ← Vite 入口，引入 HTMX
├── vite.config.ts              ← 代理 /api→Go:8080，@/ 路径别名
├── tsconfig.json               ← TS 配置
├── package.json                ← 依赖与脚本
├── public/
│   └── htmx.min.js             ← HTMX 运行时（不参与打包）
├── src/
│   ├── main.tsx                ← 应用入口（导入全局样式）
│   ├── App.tsx                 ← 根组件
│   ├── pages/                  ← 页面目录（每页一个子目录）
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

**React 与 HTMX 协作模式**（"群岛架构"）：

- HTMX 通过 `hx-get` / `hx-post` 处理页面导航和表单提交，保持"无整页刷新"体验
- React 不接管路由（不使用 React Router），作为"组件岛"嵌入页面
- 每个页面通过 `createRoot()` 挂载各自的 React 组件到 DOM 挂载点
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
