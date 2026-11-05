package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// errUploadTooLarge 表示上传的请求体超过上限。
var errUploadTooLarge = errors.New("上传文件过大")

// openUploadedFile 打开 multipart 字段 field 指向的文件，请求体整体限制在
// maxBytes 加少量 multipart 头部开销以内。
//
// 先用 MaxBytesReader 包住请求体：只看 FileHeader.Size 时，超大文件已经
// 被 multipart 解析写进临时目录，限额就形同虚设。service 层仍会按
// maxBytes 再截一次，这里是第一道闸。
func openUploadedFile(c *gin.Context, field string, maxBytes int64) (io.ReadCloser, error) {
	const multipartOverhead = 64 << 10
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+multipartOverhead)
	fh, err := c.FormFile(field)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errUploadTooLarge
		}
		return nil, err
	}
	if fh.Size > maxBytes {
		return nil, errUploadTooLarge
	}
	return fh.Open()
}
