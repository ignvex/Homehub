# HomeHub

将多种业务融为一体的个人中枢项目，集成导航页面、RSS 订阅、数据可视化等功能。计划部署在unraid服务的docker上

> 📋 项目详细元数据（技术栈、项目结构、开发约定、排除项、待讨论事项等）见 **[readme.json](readme.json)**。

## 功能模块

### 前端

- 登录页（单用户密码登录）
- 首页导航栏（搜索框 + 常用书签入口）
- 书签管理（分组、拖拽排序、图标）
- RSS 阅读界面（订阅源管理、条目列表）
- 数据可视化仪表盘
- 配置管理界面（Web 端编辑 config.json）
- 暗色模式（CSS 变量切换）

### 后端

- 用户认证（bcrypt + cookie/session）
- 日志系统（`log/slog`）
- RSS 引擎（`gofeed` 定时抓取）
- RESTful API（Gin 框架）

## 配置文件设计

```json
{
    "server": {
        "host": "0.0.0.0",
        "port": 8080
    },
    "database": {
        "host": "192.168.1.x",
        "port": 5432,
        "user": "homehub",
        "password": "your_password",
        "dbname": "homehub",
        "sslmode": "disable"
    },
    "auth": {
        "username": "admin",
        "password": "initial_password_to_be_changed",
        "session_secret": "random_64_char_string"
    },
    "log": {
        "level": "info",
        "db_writer": true
    }
}
```

- `auth.password` 仅在首次启动时用于创建初始用户
- `auth.session_secret` 用于加密 cookie，部署时务必更换
