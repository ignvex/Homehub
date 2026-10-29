package logger

import (
	"log/slog"
	"testing"
)

// TestDBHandlerWithGroupPrefixesKeys 确认 WithGroup 把分组名写进属性 key。
//
// 原实现（已删除的 dbHandlerWithGroup）既丢弃预置属性，
// 也从没把 group 写进日志字段，等于完全失效。
func TestDBHandlerWithGroupPrefixesKeys(t *testing.T) {
	h := &dbHandlerWithAttrs{groups: []string{"req"}}

	a := slog.Attr{Key: "trace", Value: slog.StringValue("t-1")}
	if got := h.prefixKey(a); got.Key != "req.trace" {
		t.Errorf("prefixKey 结果 = %q，期望 req.trace", got.Key)
	}
}

// TestDBHandlerNoGroupKeepsKeys 确认没有分组时不改动 key。
func TestDBHandlerNoGroupKeepsKeys(t *testing.T) {
	h := &dbHandlerWithAttrs{}

	a := slog.Attr{Key: "level", Value: slog.StringValue("x")}
	if got := h.prefixKey(a); got.Key != "level" {
		t.Errorf("无分组时 key 被改写为 %q", got.Key)
	}
}

// TestDBHandlerWithGroupNested 确认多级分组按 slog 语义用点号拼接。
func TestDBHandlerWithGroupNested(t *testing.T) {
	h := &dbHandlerWithAttrs{groups: []string{"a", "b"}}

	a := slog.Attr{Key: "c", Value: slog.StringValue("v")}
	if got := h.prefixKey(a); got.Key != "a.b.c" {
		t.Errorf("嵌套分组 key = %q，期望 a.b.c", got.Key)
	}
}

// TestDBHandlerWithAttrsAccumulates 确认多次 WithAttrs 叠加而非覆盖。
func TestDBHandlerWithAttrsAccumulates(t *testing.T) {
	base := &dbHandlerWithAttrs{}

	h1, ok := base.WithAttrs([]slog.Attr{{Key: "a", Value: slog.IntValue(1)}}).(*dbHandlerWithAttrs)
	if !ok {
		t.Fatal("WithAttrs 返回类型不是 *dbHandlerWithAttrs")
	}
	h2, ok := h1.WithAttrs([]slog.Attr{{Key: "b", Value: slog.IntValue(2)}}).(*dbHandlerWithAttrs)
	if !ok {
		t.Fatal("WithAttrs 返回类型不是 *dbHandlerWithAttrs")
	}

	if len(h2.attrs) != 2 {
		t.Fatalf("属性数 = %d，期望 2", len(h2.attrs))
	}
	if h2.attrs[0].Key != "a" || h2.attrs[1].Key != "b" {
		t.Errorf("属性顺序/内容不符: %v", h2.attrs)
	}
}

// TestDBHandlerWithGroupKeepsAttrs 确认带分组时预置属性不被丢弃。
func TestDBHandlerWithGroupKeepsAttrs(t *testing.T) {
	h := (&dbHandlerWithAttrs{}).WithAttrs([]slog.Attr{{Key: "a", Value: slog.IntValue(1)}})
	g, ok := h.WithGroup("req").(*dbHandlerWithAttrs)
	if !ok {
		t.Fatal("WithGroup 返回类型不是 *dbHandlerWithAttrs")
	}

	if len(g.attrs) != 1 {
		t.Fatalf("分组后属性被丢弃，剩余 %d 个", len(g.attrs))
	}
	if len(g.groups) != 1 || g.groups[0] != "req" {
		t.Errorf("分组列表 = %v，期望 [req]", g.groups)
	}
}

// TestDBHandlerEmptyGroupIsNoop 确认 slog 语义中空分组名不产生新层级。
func TestDBHandlerEmptyGroupIsNoop(t *testing.T) {
	h := &dbHandlerWithAttrs{groups: []string{"a"}}
	if got := h.WithGroup(""); got != h {
		t.Error("WithGroup(\"\") 应原样返回，不新增分组层级")
	}
}
