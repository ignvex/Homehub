package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 正文端点是列表分段的第二段，前端会把它当成「展开」的唯一来源。
// 这里的断言保护三件事：非法 ID 不落到 service、已删除的条目不回 500、
// 正常条目原样返回 HTML（前端依赖清洗后的结果直接渲染）。

func callEntryContent(t *testing.T, param string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: param}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/rss/entries/"+param+"/content", nil)
	RSSEntryContent(c)
	return w
}

func TestEntryContentRejectsNonNumericID(t *testing.T) {
	// 不初始化 svc：参数校验必须在触碰业务层之前返回，
	// 否则一个 `/entries/abc/content` 会变成 nil 解引用。
	swapServices(t, nil)
	if w := callEntryContent(t, "abc"); w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，got %d", w.Code)
	}
}

func TestEntryContentMapsMissingEntryTo404(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{RSS: service.NewRSSService(db)})

	mock.ExpectQuery(`(?s)SELECT.*content.*FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows([]string{"content"}))

	if w := callEntryContent(t, "9"); w.Code != http.StatusNotFound {
		t.Fatalf("条目已被清理时应返回 404，got %d", w.Code)
	}
}

func TestEntryContentReturnsSanitizedHTML(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{RSS: service.NewRSSService(db)})

	mock.ExpectQuery(`(?s)SELECT.*content.*FROM "rss_entries"`).
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("<p>正文</p>"))

	w := callEntryContent(t, "9")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，got %d", w.Code)
	}
	var body struct {
		Data struct {
			ID      uint64 `json:"id"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Data.ID != 9 || body.Data.Content != "<p>正文</p>" {
		t.Fatalf("响应内容不符: %+v", body.Data)
	}
}
