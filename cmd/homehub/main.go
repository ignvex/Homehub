package main

// HomeHub 个人中枢 — 集成导航页面、书签管理、RSS 订阅、待办与配置管理。
//
// 入口只负责解析命令行参数、调用 app.Run 并决定退出码；
// 启动编排在 internal/app 中，便于测试与将来被其他入口复用。
//
// @title           HomeHub API
// @version         1.1
// @description     个人中枢项目 API 接口文档，包含认证、书签管理、RSS订阅、待办事项、媒体资源、系统日志、配置管理等功能。
// @contact.name    HomeHub
// @host            localhost:8080
// @BasePath        /api/v1

import (
	"flag"
	"os"

	"github.com/ignvex/HomeHub/internal/app"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（默认取 HOMEHUB_CONFIG，未设置时为 config.json）")
	healthcheck := flag.Bool("healthcheck", false, "探测本机运行中的 HomeHub，健康时退出码为 0（供容器与编排系统的健康检查调用）")
	flag.Parse()

	if *healthcheck {
		if err := app.Healthcheck(*configPath); err != nil {
			println("homehub:", err.Error())
			os.Exit(1)
		}
		return
	}

	if err := app.Run(app.Options{ConfigPath: *configPath}); err != nil {
		// 此时日志系统可能尚未初始化，直接写 stderr。
		println("homehub:", err.Error())
		os.Exit(1)
	}
}
