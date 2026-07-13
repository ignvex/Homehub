package config

import (
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/ignvex/HomeHub/internal/logger"
)

const watchDebounce = 150 * time.Millisecond

// Watch 启动配置文件热加载监听。
//
// 在后台 goroutine 中监听 configPath 的 Write 和 Create 事件，
// 检测到变更后等待 watchDebounce 防抖，然后重新调用 Load。
// 重载失败时保留旧配置并输出错误日志，不会导致服务中断。
//
// 典型用法：
//
//	config.Load("config.json")
//	go config.Watch("config.json")
func Watch(path string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("配置文件监听器创建失败", "error", err)
		return
	}

	// 确保父目录存在后添加监听
	dir := path
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		dir = "."
	}
	if err := watcher.Add(dir); err != nil {
		logger.Error("添加配置文件监听失败", "path", path, "error", err)
		watcher.Close()
		return
	}

	go func() {
		defer watcher.Close()

		var timer *time.Timer
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// 只关心目标文件的写入/创建事件
				if event.Name != path && event.Name != "./"+path {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
					continue
				}

				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(watchDebounce, func() {
					logger.Info("检测到配置文件变更，重新加载", "path", path)
					if err := Load(path); err != nil {
						logger.Error("配置热加载失败，保留旧配置", "error", err)
					} else {
						logger.Info("配置热加载成功")
					}
				})

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Error("配置文件监听错误", "error", err)
			}
		}
	}()
}
