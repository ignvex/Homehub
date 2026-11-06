package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestReorderTooManyIDsIs400 确认三个排序接口对超长 id 列表回 400 而非 500。
//
// 这是调用方的错误；回 500 会被当成服务故障，也会在日志里留下误导性的
// 数据库错误。
func TestReorderTooManyIDsIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{
		Bookmark: service.NewBookmarkService(db),
		Todo:     service.NewTodoService(db),
	})

	ids := make([]uint64, 1001)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	editor := &model.User{BaseModel: model.BaseModel{ID: 3}, Level: model.LevelEditor}

	cases := map[string]struct {
		body map[string]any
		h    gin.HandlerFunc
	}{
		"groups":    {map[string]any{"ids": ids}, BookmarkGroupReorder},
		"bookmarks": {map[string]any{"group_id": 1, "ids": ids}, BookmarkReorder},
		"todos":     {map[string]any{"ids": ids}, TodoReorder},
	}
	for name, tc := range cases {
		raw, _ := json.Marshal(tc.body)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("user", editor)
		tc.h(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: 状态码 = %d，期望 400；body=%s", name, w.Code, w.Body.String())
		}
	}
}
