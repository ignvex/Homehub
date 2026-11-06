package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestBookmarkImportRejectsDeepNestingAs400 确认嵌套过深是调用方错误。
//
// 不映射的话会落到 default 分支回 500，前端只能显示「导入失败」，
// 看不出是文件本身的问题。
func TestBookmarkImportRejectsDeepNestingAs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{Bookmark: service.NewBookmarkService(db)})

	src := "<DL><p>" + strings.Repeat("<DT><H3>a</H3><DL><p>", 100) +
		`<DT><A HREF="https://e.example/">x</A>`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = multipartRequest(t, "file", []byte(src))
	BookmarkImport(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400；body=%s", w.Code, w.Body.String())
	}
}
