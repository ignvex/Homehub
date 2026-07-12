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

三位一体爬虫架构，覆盖 B 站视频、专栏、用户信息的自动化采集。

#### 架构

```
VideoCrawler (AV 号窗口跳跃) ──▶ videos + video_pages
ArticleCrawler (CV 号窗口跳跃) ──▶ articles
UserCrawler (种子驱动)        ──▶ bili_users
        ↑                        ↑
        └── 种子来自 videos.owner_mid + articles.mid 去重
```

- **VideoCrawler**：AV 号递增抓取，采用窗口跳跃策略（2000 个 AID 为一个窗口，窗口内随机选取，填充率达 70% 自动前移），打破线性递增的风控信号。数据写入 `videos`（主表）+ `video_pages`（分P子表），保留标题、简介、分区、UP 主、统计、Extra JSONB 等字段
- **ArticleCrawler**：CV 号递增抓取，同样采用窗口跳跃策略。数据写入 `articles` 表，正文不存（平均 20KB/篇，按需补抓），保留标题、头图、作者、统计、前后篇链表等
- **UserCrawler**：种子驱动，从已抓取的视频/专栏中收集 UP 主 mid 去重后逐条调用用户信息 API。数据写入 `bili_users` 表，包含昵称、认证、VIP、直播间、Extended JSONB 等

#### config.json 配置

```json
"bilibili": {
    "enabled": false,           // 总开关
    "video_enabled": false,     // 视频爬虫子开关
    "article_enabled": false,   // 专栏爬虫子开关
    "user_enabled": false,      // 用户爬虫子开关
    "sessdata": "",             // Cookie SESSDATA（留空=游客态）
    "bili_jct": "",
    "dede_user_id": "",
    "user_agent": ""            // 自定义 UA（为空使用默认 Chrome 桌面端）
}
```

- 总开关 `enabled` 关闭时，所有爬虫不启动
- 子开关 + `crawler_state` 表的 `Enabled` 字段双重控制，避免误启动大规模抓取
- 配置有效 SESSDATA 可大幅提高 API 请求频率上限，降低风控概率
- 窗口大小默认 2000，速率默认 2000ms/次，活跃时间窗口默认 0:00–6:00

#### API 端点

所有端点需认证（`/api/v1/bilibili/*`）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/bilibili/stats` | 存储空间预估 + 抓取进度（video/article/user 三板块） |
| GET | `/bilibili/crawlers` | 爬虫运行状态列表 |
| POST | `/bilibili/fetch/video/:aid` | 按 AID 即时抓取单个视频（含全部分 P） |
| POST | `/bilibili/fetch/article/:cvid` | 按 CVID 即时抓取单个专栏 |

详细 API 文档见 `docs/swagger.yaml`。
