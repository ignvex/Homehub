package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestSearchInternalErrorHidesDriverMessage 防的是把 SQL 错误原文回给客户端。
//
// 驱动错误里会带表名、列名甚至语句片段，等于向调用方描述 schema。
// 500 时只能回通用文案，原文进日志。
func TestSearchInternalErrorHidesDriverMessage(t *testing.T) {
	db, mock, cleanup := testutil.NewUnorderedMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{Search: service.NewSearchService(db)})

	const secret = `relation "secret_table_xyz" does not exist`
	for range 8 {
		mock.ExpectQuery(`.*`).WillReturnError(errors.New(secret))
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/search?q=hello", nil)
	SearchAll(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("期望 500，got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret_table_xyz") {
		t.Fatalf("响应泄露了内部错误原文: %s", w.Body.String())
	}
}
