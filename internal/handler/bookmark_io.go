package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/service"
)

// BookmarkExport 导出全部书签。
//
//	@Summary		导出书签
//	@Description	导出为 Netscape Bookmark File（浏览器通用 HTML），每个分组一个 H3 文件夹；以附件 homehub-bookmarks.html 下载
//	@Tags			书签
//	@Produce		html
//	@Success		200	{string}	string		"书签 HTML"
//	@Failure		500	{object}	Response	"服务器内部错误"
//	@Router			/bookmarks/export [get]
func BookmarkExport(c *gin.Context) {
	out, err := svc.Bookmark.ExportBookmarksHTML()
	if err != nil {
		FailInternal(c, "导出书签失败")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="homehub-bookmarks.html"`)
	c.Data(http.StatusOK, "text/html; charset=utf-8", out)
}

// BookmarkImport 从浏览器导出的书签文件导入。
//
//	@Summary		导入书签
//	@Description	multipart 字段 file（≤5MB），Netscape Bookmark File 格式。H3 文件夹作为分组（嵌套拍平为「父/子」），不在文件夹里的归入「导入的书签」；同组同 URL 去重，只接受 http/https
//	@Tags			书签
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			file	formData	file	true	"书签 HTML 文件"
//	@Success		200		{object}	Response{data=service.BookmarkImportResult}	"导入结果"
//	@Failure		400		{object}	Response	"文件缺失、过大或无法解析"
//	@Router			/bookmarks/import [post]
func BookmarkImport(c *gin.Context) {
	f, err := openUploadedFile(c, "file", service.MaxBookmarkImportBytes)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			Fail(c, http.StatusBadRequest, CodeFileTooLarge, service.ErrBookmarkImportTooLarge.Error())
			return
		}
		FailBadRequest(c, "请选择要导入的书签文件（字段 file）")
		return
	}
	defer f.Close()

	res, err := svc.Bookmark.ImportBookmarksHTML(f)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrBookmarkImportTooLarge):
			Fail(c, http.StatusBadRequest, CodeFileTooLarge, err.Error())
		case errors.Is(err, service.ErrBookmarkImportTooMany):
			FailBadRequest(c, err.Error())
		default:
			FailInternal(c, "导入书签失败")
		}
		return
	}
	Success(c, res)
}
