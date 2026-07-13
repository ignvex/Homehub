package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
)

// ConfigGet 获取安全配置（过滤敏感字段）。
//
// GET /api/v1/config
//
// @Summary      获取应用配置
// @Description  返回当前应用配置（已过滤密码等敏感字段）
// @Tags         配置
// @Produce      json
// @Success      200  {object}  Response{data=service.SafeConfig}  "配置内容"
// @Router       /config [get]
func ConfigGet(c *gin.Context) {
	cfg := svc.Config.GetSafe()
	Success(c, cfg)
}

// ConfigUpdate 通过 Web 端更新配置。
//
// PUT /api/v1/config
//
// @Summary      更新应用配置
// @Description  通过 Web 端部分更新 config.json，更新后触发热重载
// @Tags         配置
// @Accept       json
// @Produce      json
// @Param        request  body  service.UpdateConfigRequest  true  "配置更新字段"
// @Success      200  {object}  Response  "更新成功"
// @Failure      400  {object}  Response  "参数错误"
// @Router       /config [put]
func ConfigUpdate(c *gin.Context) {
	var req service.UpdateConfigRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的配置参数")
		return
	}

	if err := svc.Config.Update(req); err != nil {
		FailBadRequest(c, err.Error())
		return
	}

	Success(c, nil)
}
