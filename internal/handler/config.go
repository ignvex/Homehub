package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
)

// ConfigGet 获取安全配置（过滤敏感字段）。
//
// GET /api/v1/config
func ConfigGet(c *gin.Context) {
	cfg := svc.Config.GetSafe()
	Success(c, cfg)
}

// ConfigUpdate 通过 Web 端更新配置。
//
// PUT /api/v1/config
func ConfigUpdate(c *gin.Context) {
	var req service.UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "无效的配置参数")
		return
	}

	if err := svc.Config.Update(req); err != nil {
		FailBadRequest(c, err.Error())
		return
	}

	Success(c, nil)
}
