package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
)

// TestLogStreamPreservesInstant 锁住一条时区回归。
//
// SSE 曾经用无时区的 "2006-01-02T15:04:05" 输出 log_time，而列表 API 走的是
// time.Time 的 RFC3339。同一个字段两种格式：前端用 new Date() 解析 SSE 那一份
// 时会按**浏览器本地时区**解释，但它实际是**服务器本地时区**的时间。两者不同时区
// 时，推送进来的行显示时刻整体偏移，matchesQuery 的 since/until 判断也随之失真。
//
// 断言方式刻意选择"解析回时刻再比较"而不是"比较字符串"：只要输出带上了时区，
// 偏移量写成 +08:00 还是 Z 都无所谓，语义相同即可。
func TestLogStreamPreservesInstant(t *testing.T) {
	cases := []struct {
		name  string
		stamp time.Time
	}{
		{"东八区", time.Date(2026, 10, 31, 19, 30, 45, 0, time.FixedZone("CST", 8*3600))},
		{"UTC", time.Date(2026, 10, 31, 11, 30, 45, 123456789, time.UTC)},
		{"西五区", time.Date(2026, 10, 31, 6, 30, 45, 0, time.FixedZone("EST", -5*3600))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &model.LogEntry{BaseModel: model.BaseModel{ID: 1}, Level: "INFO", LogTime: tc.stamp}

			raw, err := json.Marshal(toLogEntryJSON(entry))
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}

			var got struct {
				LogTime string `json:"log_time"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("反序列化失败: %v", err)
			}

			// 无时区的串无法按 RFC3339 解析，会直接在这里失败。
			parsed, err := time.Parse(time.RFC3339, got.LogTime)
			if err != nil {
				t.Fatalf("log_time = %q 不是 RFC3339（缺少时区信息？）: %v", got.LogTime, err)
			}
			if !parsed.Equal(tc.stamp) {
				t.Errorf("log_time 解析为 %s，期望 %s（原始 %q）",
					parsed.Format(time.RFC3339Nano), tc.stamp.Format(time.RFC3339Nano), got.LogTime)
			}
		})
	}
}

// TestLogStreamMatchesListFormat 确认 SSE 与列表 API 对 log_time 的编码一致。
//
// 同一份数据经两个端点出去必须是同一种格式，否则前端需要为其中一个写特判，
// 而这正是本次回归的成因。
func TestLogStreamMatchesListFormat(t *testing.T) {
	stamp := time.Date(2026, 10, 31, 19, 30, 45, 0, time.FixedZone("CST", 8*3600))
	entry := &model.LogEntry{BaseModel: model.BaseModel{ID: 7}, Level: "WARN", Message: "m", LogTime: stamp}

	streamRaw, err := json.Marshal(toLogEntryJSON(entry))
	if err != nil {
		t.Fatalf("SSE 序列化失败: %v", err)
	}
	listRaw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("列表序列化失败: %v", err)
	}

	var streamFields struct {
		LogTime string `json:"log_time"`
	}
	var listFields struct {
		LogTime string `json:"log_time"`
	}
	if err := json.Unmarshal(streamRaw, &streamFields); err != nil {
		t.Fatalf("SSE 反序列化失败: %v", err)
	}
	if err := json.Unmarshal(listRaw, &listFields); err != nil {
		t.Fatalf("列表反序列化失败: %v", err)
	}

	if streamFields.LogTime != listFields.LogTime {
		t.Errorf("SSE log_time = %q，列表 log_time = %q，两者应完全一致",
			streamFields.LogTime, listFields.LogTime)
	}
}
