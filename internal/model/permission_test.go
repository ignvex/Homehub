package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHasLevel 验证“数值小 = 权限高”的比较语义。
func TestHasLevel(t *testing.T) {
	cases := []struct {
		userLevel int
		required  int
		want      bool
	}{
		{LevelAdmin, LevelAdmin, true},
		{LevelAdmin, LevelEditor, true},
		{LevelAdmin, LevelViewer, true},
		{LevelEditor, LevelAdmin, false},
		{LevelEditor, LevelEditor, true},
		{LevelEditor, LevelMember, true},
		{LevelViewer, LevelMember, false},
		{LevelViewer, LevelViewer, true},
	}
	for _, c := range cases {
		u := &User{Level: c.userLevel}
		if got := u.HasLevel(c.required); got != c.want {
			t.Errorf("level=%d HasLevel(%d) = %v, want %v",
				c.userLevel, c.required, got, c.want)
		}
	}
}

// TestHasLevelRejectsInvalidLevels 锁住一条 fail-open 回归。
//
// 早先的实现只判 `u.Level <= required`，于是 Level=0 满足 `0 <= 1` ——
// 一个从未被正确初始化过的账号反而拿到最高权限。这不是理论风险：
// 数据库默认值、JSON 缺字段、迁移漏列都可能产生 0。
func TestHasLevelRejectsInvalidLevels(t *testing.T) {
	for _, invalid := range []int{0, -1, 6, 99} {
		u := &User{Level: invalid}
		if u.HasLevel(LevelViewer) {
			t.Errorf("非法等级 %d 竟然通过了最低权限校验", invalid)
		}
		if u.IsAdmin() {
			t.Errorf("非法等级 %d 竟然被当成管理员", invalid)
		}
	}
}

// TestHasLevelNilUser 确认 nil 用户不会 panic 且恒为 false。
func TestHasLevelNilUser(t *testing.T) {
	var u *User
	if u.HasLevel(LevelViewer) {
		t.Error("nil 用户不应有任何权限")
	}
	if u.IsAdmin() {
		t.Error("nil 用户不应是管理员")
	}
}

// TestCanManage 验证层级管理规则。
func TestCanManage(t *testing.T) {
	admin := &User{BaseModel: BaseModel{ID: 1}, Level: LevelAdmin}
	op := &User{BaseModel: BaseModel{ID: 2}, Level: LevelOperator}
	peer := &User{BaseModel: BaseModel{ID: 3}, Level: LevelOperator}
	viewer := &User{BaseModel: BaseModel{ID: 4}, Level: LevelViewer}

	if !admin.CanManage(op) {
		t.Error("1 级应能管 2 级")
	}
	if !admin.CanManage(viewer) {
		t.Error("1 级应能管 5 级")
	}
	if op.CanManage(admin) {
		t.Error("2 级不应能管 1 级")
	}
	if op.CanManage(peer) {
		t.Error("同级不应互相管理（避免互相停用）")
	}
	if op.CanManage(op) {
		t.Error("不应能管自己")
	}
	if !op.CanManage(viewer) {
		t.Error("2 级应能管 5 级")
	}
}

// TestCanManageNil 确认 nil 安全。
func TestCanManageNil(t *testing.T) {
	u := &User{Level: LevelAdmin}
	if u.CanManage(nil) {
		t.Error("nil target 不应可管理")
	}
	var n *User
	if n.CanManage(u) {
		t.Error("nil operator 不应能管理")
	}
}

// TestLevelFromRole 验证旧 role 字符串的映射。
func TestLevelFromRole(t *testing.T) {
	cases := map[string]int{
		"admin":  LevelAdmin,
		"user":   LevelViewer,
		"":       LevelViewer, // 认不出的一律最低
		"bogus":  LevelViewer,
		"editor": LevelViewer, // 旧值里不存在，按最低处理
	}
	for role, want := range cases {
		if got := LevelFromRole(role); got != want {
			t.Errorf("LevelFromRole(%q) = %d, want %d", role, got, want)
		}
	}
}

// TestNormalizeLevel 确认夹紧到合法区间。
func TestNormalizeLevel(t *testing.T) {
	cases := map[int]int{
		0:  LevelMin,
		-5: LevelMin,
		1:  1,
		3:  3,
		5:  5,
		6:  LevelMax,
		99: LevelMax,
	}
	for in, want := range cases {
		if got := NormalizeLevel(in); got != want {
			t.Errorf("NormalizeLevel(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestAllLevelsCoverage 确认 5 个等级都有名字与说明。
func TestAllLevelsCoverage(t *testing.T) {
	levels := AllLevels()
	if len(levels) != 5 {
		t.Fatalf("应有 5 个等级，实际 %d", len(levels))
	}
	for _, l := range levels {
		if !IsValidLevel(l.Level) {
			t.Errorf("等级 %d 不合法", l.Level)
		}
		if l.Name == "" {
			t.Errorf("等级 %d 缺少名称", l.Level)
		}
		if l.Description == "" {
			t.Errorf("等级 %d 缺少说明", l.Level)
		}
	}
	if levels[0].Level != LevelAdmin {
		t.Errorf("第一个等级应是 admin(1)，实际 %d", levels[0].Level)
	}
	if LevelName(LevelAdmin) != "admin" {
		t.Errorf("1 级名称应为 admin，实际 %q", LevelName(LevelAdmin))
	}
}

// TestUserMarshalJSONIncludesRole 确认 role 由 Level 派生输出。
//
// 前端仍在按 "admin"/"user" 判断，这里保证改造后它不会立刻坏掉。
func TestUserMarshalJSONIncludesRole(t *testing.T) {
	cases := []struct {
		level int
		role  string
	}{
		{LevelAdmin, "admin"},
		{LevelOperator, "operator"},
		{LevelEditor, "editor"},
		{LevelMember, "member"},
		{LevelViewer, "viewer"},
	}

	for _, c := range cases {
		u := User{Level: c.level, Username: "x"}
		raw, err := u.MarshalJSON()
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("反序列化失败: %v", err)
		}
		if out["role"] != c.role {
			t.Errorf("level=%d 的 role = %v, want %q", c.level, out["role"], c.role)
		}
		if lv, ok := out["level"].(float64); !ok || int(lv) != c.level {
			t.Errorf("level 字段缺失或错误: %v", out["level"])
		}
	}
}

// TestUserMarshalJSONHidesPasswordHash 确认密码哈希不会泄露。
func TestUserMarshalJSONHidesPasswordHash(t *testing.T) {
	u := User{Level: LevelAdmin, PasswordHash: "super-secret-hash"}
	raw, err := u.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-hash") {
		t.Error("password_hash 泄露到了 JSON 中")
	}
}
