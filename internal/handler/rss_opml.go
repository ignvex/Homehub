package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/service"
)

// RSSExportOPML 导出全部订阅源为 OPML。
//
//	@Summary		导出 OPML
//	@Description	导出全部订阅源为 OPML 2.0，分组嵌套为一级 outline，未分组的放顶层；以附件 homehub-feeds.opml 下载
//	@Tags			RSS 订阅
//	@Produce		xml
//	@Success		200	{string}	string		"OPML 文档"
//	@Failure		500	{object}	Response	"服务器内部错误"
//	@Router			/rss/opml [get]
func RSSExportOPML(c *gin.Context) {
	out, err := svc.RSS.ExportOPML()
	if err != nil {
		FailInternal(c, "导出 OPML 失败")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="homehub-feeds.opml"`)
	c.Data(http.StatusOK, "text/x-opml; charset=utf-8", out)
}

// RSSImportOPML 从 OPML 文件导入订阅源。
//
//	@Summary		导入 OPML
//	@Description	multipart 字段 file（≤2MB，≤2000 个源）。按 URL 去重，分组按名字匹配或新建；只保存不抓取，由后台调度抓取
//	@Tags			RSS 订阅
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			file	formData	file	true	"OPML 文件"
//	@Success		200		{object}	Response{data=service.OPMLImportResult}	"导入结果"
//	@Failure		400		{object}	Response	"文件缺失、过大或无法解析"
//	@Router			/rss/opml [post]
func RSSImportOPML(c *gin.Context) {
	f, err := openUploadedFile(c, "file", service.MaxOPMLBytes)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			Fail(c, http.StatusBadRequest, CodeFileTooLarge, service.ErrOPMLTooLarge.Error())
			return
		}
		FailBadRequest(c, "请选择要导入的 OPML 文件（字段 file）")
		return
	}
	defer f.Close()

	res, err := svc.RSS.ImportOPML(f)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOPMLTooLarge):
			Fail(c, http.StatusBadRequest, CodeFileTooLarge, err.Error())
		case errors.Is(err, service.ErrOPMLInvalid), errors.Is(err, service.ErrOPMLTooManyURL):
			FailBadRequest(c, err.Error())
		default:
			FailInternal(c, "导入 OPML 失败")
		}
		return
	}
	Success(c, res)
}
