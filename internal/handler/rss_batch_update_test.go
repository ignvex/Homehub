package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// 批量请求体复用了单条更新的字段定义（匿名内嵌）。内嵌结构体在
// encoding/json 里会提升字段，但如果哪天有人把它改成命名字段，
// `{"ids":[1],"fetch_by":"extension"}` 会静默绑成空 patch——
// 请求成功、什么都没改。这条断言直接检查绑定结果。

func bindBatch(t *testing.T, body string) batchUpdateRSSFeedsRequest {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/rss/feeds/batch", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	var req batchUpdateRSSFeedsRequest
	if err := c.ShouldBind(&req); err != nil {
		t.Fatalf("绑定请求失败: %v", err)
	}
	return req
}

func TestBatchRequestLiftsEmbeddedFields(t *testing.T) {
	req := bindBatch(t, `{"ids":[3,9],"fetch_by":"extension","fetch_enabled":false,"fetch_interval":900}`)
	if len(req.IDs) != 2 || req.IDs[0] != 3 || req.IDs[1] != 9 {
		t.Fatalf("ids 未绑定: %+v", req.IDs)
	}
	if req.FetchBy == nil || *req.FetchBy != "extension" {
		t.Fatal("fetch_by 未提升到顶层")
	}
	if req.FetchEnabled == nil || *req.FetchEnabled {
		t.Fatal("fetch_enabled 未绑定")
	}
	if req.FetchInterval == nil || *req.FetchInterval != 900 {
		t.Fatal("fetch_interval 未绑定")
	}
}

// group_id 的三态必须在批量请求里同样成立：
// 缺省=不动、null=清空、数字=移入。丢掉任何一态，
// 「批量移出分组」或「批量移入」就会有一个做不到。
func TestBatchRequestKeepsGroupTriState(t *testing.T) {
	absent := bindBatch(t, `{"ids":[1]}`)
	if absent.GroupID.Set {
		t.Fatal("缺省 group_id 不该被当成已设置")
	}

	cleared := bindBatch(t, `{"ids":[1],"group_id":null}`)
	if !cleared.GroupID.Set || cleared.GroupID.Value != nil {
		t.Fatal("显式 null 应表示清空分组")
	}

	moved := bindBatch(t, `{"ids":[1],"group_id":7}`)
	if !moved.GroupID.Set || moved.GroupID.Value == nil || *moved.GroupID.Value != 7 {
		t.Fatal("数字应表示移入指定分组")
	}
}

// 空 ids 必须在 handler 层就被拒，不能带着空列表进 service。
func TestBatchHandlerRejectsEmptyIDs(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{RSS: service.NewRSSService(db)})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/rss/feeds/batch", strings.NewReader(`{"ids":[],"fetch_enabled":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	RSSBatchUpdateFeeds(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("空 ids 应返回 400，got %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("空 ids 不该触达数据库: %v", err)
	}
}

// 正常路径：一次事务、一条 IN 更新，返回实际更新数量。
func TestBatchHandlerReportsUpdatedCount(t *testing.T) {
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{RSS: service.NewRSSService(db)})

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "rss_feeds" SET .*WHERE id IN \(\$3,\$4\)`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/rss/feeds/batch",
		strings.NewReader(`{"ids":[4,5],"fetch_enabled":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	RSSBatchUpdateFeeds(c)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Updated int64 `json:"updated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Data.Updated != 2 {
		t.Fatalf("期望 updated=2，got %d", body.Data.Updated)
	}
}

// swapServices 替换包级 svc，并在测试结束后还原。
//
// 放在这个文件里是因为它被多个 handler 测试共用；先提交的文件
// 必须自带依赖，否则中间提交的 `go vet` 会因为找不到符号而失败。
func swapServices(t *testing.T, next *service.Services) {
	t.Helper()
	prev := svc
	svc = next
	t.Cleanup(func() { svc = prev })
}
