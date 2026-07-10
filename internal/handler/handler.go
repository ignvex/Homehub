// Package handler 提供 HomeHub HTTP 请求处理器。
//
// 遵循"薄处理器"原则：参数绑定 → 调用 service → 统一响应。
// 通过 Init 注入 *service.Services，各 handler 函数通过包级 svc 变量访问业务层。
package handler

import "github.com/ignvex/HomeHub/internal/service"

var svc *service.Services

// Init 注入业务服务层实例。
//
// 应在 main.go 中、创建 Services 后立即调用。
func Init(s *service.Services) {
	svc = s
}
