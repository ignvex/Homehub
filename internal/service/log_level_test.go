package service

import (
	"strings"
	"testing"
)

// TestParseLevels 验证多级别解析。
func TestParseLevels(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"ERROR", []string{"ERROR"}, false},
		{"error", []string{"ERROR"}, false},
		{"ERROR,WARN", []string{"ERROR", "WARN"}, false},
		{"error, warn , info", []string{"ERROR", "WARN", "INFO"}, false},
		{"ERROR,ERROR", []string{"ERROR"}, false}, // 去重
		{"FOO", nil, true},
		{"ERROR,FOO", nil, true},
	}

	for _, c := range cases {
		got, err := ParseLevels(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLevels(%q) 应报错，实际返回 %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLevels(%q) 意外报错: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("ParseLevels(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseLevels(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// TestParseLevelsRejectsInvalid 锁住“静默返回全部”这条老问题。
//
// 早先的实现里，level=FOO 会被跳过、不加任何 WHERE，于是接口返回
// 全部日志 —— 调用方以为过滤生效了，实际没有。排查线上问题时
// 这种“看起来对但没生效”的行为比报错危险得多。
func TestParseLevelsRejectsInvalid(t *testing.T) {
	if _, err := ParseLevels("FOO"); err == nil {
		t.Fatal("非法级别应返回错误，而不是静默忽略")
	}
	if _, err := ParseLevels("ERROR,FOO"); err == nil {
		t.Fatal("含非法值的列表应整体报错")
	}
}

// TestLevelsAtLeast 验证“至少这么高”的语义。
func TestLevelsAtLeast(t *testing.T) {
	cases := []struct {
		min  string
		want map[string]bool
	}{
		{"ERROR", map[string]bool{"ERROR": true}},
		{"WARN", map[string]bool{"WARN": true, "ERROR": true}},
		{"INFO", map[string]bool{"INFO": true, "WARN": true, "ERROR": true}},
		{"DEBUG", map[string]bool{"DEBUG": true, "INFO": true, "WARN": true, "ERROR": true}},
	}

	for _, c := range cases {
		got, err := LevelsAtLeast(c.min)
		if err != nil {
			t.Fatalf("LevelsAtLeast(%q) 报错: %v", c.min, err)
		}
		if len(got) != len(c.want) {
			t.Errorf("LevelsAtLeast(%q) = %v, want %v", c.min, got, c.want)
			continue
		}
		for _, lv := range got {
			if !c.want[lv] {
				t.Errorf("LevelsAtLeast(%q) 包含了不该有的 %q", c.min, lv)
			}
		}
	}
}

// TestLevelsAtLeastInvalid 确认非法下限报错。
func TestLevelsAtLeastInvalid(t *testing.T) {
	if _, err := LevelsAtLeast("TRACE"); err == nil {
		t.Fatal("非法最低级别应报错")
	}
	if _, err := LevelsAtLeast(""); err != nil {
		t.Errorf("空值应视为不过滤，实际报错: %v", err)
	}
}

// TestLevelsAtLeastCoversAll 确认每个下限都能解析出全部更严重的级别。
func TestLevelsAtLeastCoversAll(t *testing.T) {
	all := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	for _, min := range all {
		got, err := LevelsAtLeast(min)
		if err != nil {
			t.Fatalf("%q 报错: %v", min, err)
		}
		if len(got) == 0 {
			t.Errorf("%q 解析出 0 个级别", min)
		}
		// 结果里的每个级别，严重程度都必须 >= min
		for _, lv := range got {
			if levelRank[lv] < levelRank[min] {
				t.Errorf("%q 的结果里出现了更轻的级别 %q", min, lv)
			}
		}
	}
}

// TestLevelRankIsTotalOrder 确认等级序完整且严格递增。
func TestLevelRankIsTotalOrder(t *testing.T) {
	order := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	prev := 0
	for _, lv := range order {
		r, ok := levelRank[lv]
		if !ok {
			t.Fatalf("缺少级别 %q 的序值", lv)
		}
		if r <= prev {
			t.Errorf("%q 的序值 %d 未严格大于前一个 %d", lv, r, prev)
		}
		prev = r
	}
	if len(levelRank) != len(validLogLevels) {
		t.Errorf("levelRank(%d) 与 validLogLevels(%d) 不一致：有级别没登记序值",
			len(levelRank), len(validLogLevels))
	}
}

// TestLogQueryErrorMessages 确认错误信息包含可操作提示。
func TestLogQueryErrorMessages(t *testing.T) {
	_, err := ParseLevels("BAD")
	if err == nil {
		t.Fatal("应报错")
	}
	// 报错时应告诉调用方可选值，否则它只能去翻代码
	if !strings.Contains(err.Error(), "DEBUG") || !strings.Contains(err.Error(), "ERROR") {
		t.Errorf("错误信息应列出可选级别，实际: %v", err)
	}
}
