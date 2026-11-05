package handler

import (
	"encoding/json"
	"testing"
)

// 三态绑定：缺失 / null / 空串 / 值。缺失与 null 都绑成 nil 指针时，
// 「清空描述」与「不改描述」无法区分——这正是改造前清不掉的原因。
func TestUpdateTodoRequestTriState(t *testing.T) {
	cases := []struct {
		body             string
		descSet, descNil bool
		dueSet, dueNil   bool
	}{
		{`{}`, false, true, false, true},
		{`{"description":null,"due_date":null}`, true, true, true, true},
		{`{"description":"","due_date":""}`, true, true, true, true},
		{`{"description":"x","due_date":"2026-11-05T10:00:00Z"}`, true, false, true, false},
	}
	for _, tc := range cases {
		var req updateTodoRequest
		if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if req.Description.Set != tc.descSet || (req.Description.Value == nil) != tc.descNil {
			t.Errorf("%s: description = %+v", tc.body, req.Description)
		}
		if req.DueDate.Set != tc.dueSet || (req.DueDate.Value == nil) != tc.dueNil {
			t.Errorf("%s: due_date = %+v", tc.body, req.DueDate)
		}
	}
	var req updateTodoRequest
	if err := json.Unmarshal([]byte(`{"due_date":"tomorrow"}`), &req); err == nil {
		t.Error("非 RFC3339 的 due_date 应报错")
	}
}
