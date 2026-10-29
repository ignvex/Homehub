// Package homehub 是 HomeHub 项目的根包，本身不包含可执行代码。
//
// 它的存在有两个作用：
//
//  1. 让 `go list .` 在仓库根目录可用。工具链（尤其是 swag）需要根目录
//     是一个有效 Go 包，才能把 internal/* 解析成短包名（service、model），
//     否则注解里的 `service.TargetResult` 之类引用会无法解析。
//  2. 为 `go doc homehub` 提供一个项目级说明的落脚点。
//
// 可执行入口在 cmd/homehub。
package homehub
