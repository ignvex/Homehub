// Package web 把前端构建产物嵌入二进制。
//
// 部署原本要同时同步 homehub 与 web/dist，两者版本错开时页面会引用不存在的
// 哈希资源而白屏。嵌入后发布物只有一个文件。
//
// go:embed 只能引用本包目录及其子目录，所以这个文件必须放在 web/ 下，
// 与 dist/ 同级。构建顺序因此固定为：先 pnpm build，再 go build。
//
// web/dist/.gitkeep 是提交进仓库的占位文件：没有它，干净检出时 dist 目录
// 不存在，go build 会直接失败。只有占位文件时服务端会提示先构建前端。
// vite 的 emptyOutDir 会清空 dist，所以 vite.config.ts 在构建后重新写入它。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回以 dist/ 为根的前端文件系统（index.html 位于根）。
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// "dist" 是编译期常量路径，fs.Sub 只会在路径非法时出错。
		panic(err)
	}
	return sub
}
