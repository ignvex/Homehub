package handler

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func multipartRequest(t *testing.T, field string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, "upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/x", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// 上传上限必须在解析 multipart 时就生效：只比 FileHeader.Size 的话，
// 超大文件已经整份落到临时目录，限额只是事后统计。
func TestOpenUploadedFileEnforcesLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = multipartRequest(t, "file", bytes.Repeat([]byte("a"), 300<<10))
	if _, err := openUploadedFile(c, "file", 100<<10); !errors.Is(err, errUploadTooLarge) {
		t.Fatalf("超限文件应返回 errUploadTooLarge，got %v", err)
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = multipartRequest(t, "file", []byte("hello"))
	f, err := openUploadedFile(c, "file", 100<<10)
	if err != nil {
		t.Fatalf("小文件应可打开: %v", err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if !strings.EqualFold(string(got), "hello") {
		t.Errorf("内容 = %q", got)
	}
}
