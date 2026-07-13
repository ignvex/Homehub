package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 媒体资源 API
// ============================================================

// MediaList 获取所有媒体资源列表。
//
// GET /api/v1/media-assets
//
// @Summary      获取媒体资源列表
// @Description  返回所有已上传媒体资源的元数据列表
// @Tags         媒体资源
// @Produce      json
// @Success      200  {object}  Response{data=[]model.MediaAsset}  "资源列表"
// @Router       /media-assets [get]
func MediaList(c *gin.Context) {
	assets, err := svc.Media.List()
	if err != nil {
		FailInternal(c, "查询媒体资源列表失败")
		return
	}
	Success(c, assets)
}

// MediaUpload 上传媒体资源。
//
// POST /api/v1/media-assets
//
// @Summary      上传媒体资源
// @Description  上传图片或视频文件，支持 jpg/png/gif/webp/mp4/webm 等格式
// @Tags         媒体资源
// @Accept       multipart/form-data
// @Produce      json
// @Param        file  formData  file  true  "上传文件"
// @Success      200   {object}  Response{data=model.MediaAsset}  "上传成功"
// @Failure      400   {object}  Response  "文件过大或格式不支持"
// @Router       /media-assets [post]
func MediaUpload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		FailBadRequest(c, "请选择要上传的文件")
		return
	}

	asset, err := svc.Media.Upload(file)
	if err != nil {
		if errors.Is(err, service.ErrFileTooLarge) {
			Fail(c, http.StatusBadRequest, CodeFileTooLarge, "文件大小不能超过 50MB")
			return
		}
		if errors.Is(err, service.ErrInvalidFileType) {
			Fail(c, http.StatusBadRequest, CodeInvalidFileType, "不支持的文件类型，仅支持常见图片和视频格式")
			return
		}
		FailInternal(c, "上传失败")
		return
	}

	Success(c, asset)
}

// MediaDelete 删除媒体资源。
//
// DELETE /api/v1/media-assets/:id
//
// @Summary      删除媒体资源
// @Description  删除媒体资源及其物理文件
// @Tags         媒体资源
// @Produce      json
// @Param        id  path  int  true  "资源 ID"
// @Success      200  {object}  Response  "删除成功"
// @Failure      400  {object}  Response  "无效的资源 ID"
// @Failure      404  {object}  Response  "资源不存在"
// @Router       /media-assets/{id} [delete]
func MediaDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的资源 ID")
		return
	}

	if err := svc.Media.Delete(id); err != nil {
		if errors.Is(err, service.ErrAssetNotFound) {
			Fail(c, http.StatusNotFound, CodeMediaNotFound, "媒体资源不存在")
			return
		}
		FailInternal(c, "删除失败")
		return
	}

	Success(c, nil)
}
