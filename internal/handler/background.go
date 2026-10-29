package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 背景设置 API
// ============================================================

// BackgroundGet 获取当前背景设置。
//
// GET /api/v1/background
//
// @Summary      获取背景设置
// @Description  返回当前页面背景的显示模式、间隔、选中资源等配置
// @Tags         背景
// @Produce      json
// @Success      200  {object}  Response{data=service.BackgroundSettings}  "背景设置"
// @Router       /background [get]
func BackgroundGet(c *gin.Context) {
	settings := svc.Background.Get()
	Success(c, settings)
}

// BackgroundUpdate 更新背景设置。
//
// PUT /api/v1/background
//
// @Summary      更新背景设置
// @Description  修改背景显示模式、切换间隔、选中资源等配置
// @Tags         背景
// @Accept       json
// @Produce      json
// @Param        request  body  service.UpdateBackgroundRequest  true  "背景设置更新"
// @Success      200  {object}  Response  "更新成功"
// @Failure      400  {object}  Response  "参数错误"
// @Router       /background [put]
func BackgroundUpdate(c *gin.Context) {
	var req service.UpdateBackgroundRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的背景设置参数")
		return
	}

	// 背景只接受图片与视频：前端即便提交了音频/PDF 的 ID，
	// 也要在持久化前剔除，否则首页与登录页会渲染出错位画面。
	if req.SelectedAssetIDs != nil {
		filtered, err := svc.Media.FilterBackgroundIDs(*req.SelectedAssetIDs)
		if err != nil {
			FailInternal(c, "校验背景素材失败")
			return
		}
		req.SelectedAssetIDs = &filtered
	}

	if err := svc.Background.Update(req); err != nil {
		FailBadRequest(c, err.Error())
		return
	}

	Success(c, nil)
}
