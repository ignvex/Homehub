package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bindFeedRequest 走真实的 gin 绑定路径解析请求体，而不是自己 json.Unmarshal。
//
// 这里必须用真实的绑定器：回归就藏在"JSON null 绑定成什么"这一层，
// 绕过它写断言等于没测。
func bindFeedRequest(t *testing.T, body string) updateRSSFeedRequest {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/rss/feeds/1", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	var req updateRSSFeedRequest
	if err := c.ShouldBind(&req); err != nil {
		t.Fatalf("绑定请求失败: %v", err)
	}
	return req
}

// TestUpdateFeedRequestDistinguishesNullFromAbsent 锁住分组清除的绑定语义。
//
// 这是"选未分组清不掉"的上游成因：`*uint64` 对缺失字段和显式 null 都给出
// nil，两者语义完全相反 —— 前者是"别动分组"，后者是"把分组清空"。
// 请求结构必须能分开表达，否则后面几层再怎么写都补不回来。
func TestUpdateFeedRequestDistinguishesNullFromAbsent(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSet bool
		wantNil bool
		wantVal uint64
	}{
		{
			name:    "只改标题时不动分组",
			body:    `{"title":"新标题"}`,
			wantSet: false,
		},
		{
			name:    "显式 null 表示清空分组",
			body:    `{"group_id":null}`,
			wantSet: true,
			wantNil: true,
		},
		{
			name:    "数字表示移入分组",
			body:    `{"group_id":7}`,
			wantSet: true,
			wantVal: 7,
		},
		{
			name:    "空对象不修改任何字段",
			body:    `{}`,
			wantSet: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := bindFeedRequest(t, tc.body)

			if req.GroupID.Set != tc.wantSet {
				t.Fatalf("GroupID.Set = %v，期望 %v（body=%s）", req.GroupID.Set, tc.wantSet, tc.body)
			}
			if !tc.wantSet {
				return
			}
			if tc.wantNil {
				if req.GroupID.Value != nil {
					t.Errorf("GroupID.Value = %v，期望 nil", *req.GroupID.Value)
				}
				return
			}
			if req.GroupID.Value == nil {
				t.Fatalf("GroupID.Value = nil，期望 %d", tc.wantVal)
			}
			if *req.GroupID.Value != tc.wantVal {
				t.Errorf("GroupID.Value = %d，期望 %d", *req.GroupID.Value, tc.wantVal)
			}
		})
	}
}

// TestUpdateFeedRequestRejectsMalformedGroupID 确认非法值仍会被拒绝。
//
// 自定义 UnmarshalJSON 绕过了标准库的常规校验路径，容易顺手把类型检查也
// 弄丢。负数和非数字都必须报错，而不是静默变成 0 或 nil。
func TestUpdateFeedRequestRejectsMalformedGroupID(t *testing.T) {
	for _, body := range []string{`{"group_id":-1}`, `{"group_id":"abc"}`, `{"group_id":1.5}`} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/rss/feeds/1", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			var req updateRSSFeedRequest
			if err := c.ShouldBind(&req); err == nil {
				t.Errorf("body=%s 应当绑定失败，实际成功（Set=%v）", body, req.GroupID.Set)
			}
		})
	}
}
