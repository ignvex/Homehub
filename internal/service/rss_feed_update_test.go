package service

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ignvex/HomeHub/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func uint64Ptr(v uint64) *uint64 { return &v }

// TestBuildFeedUpdatesDistinguishesClearFromSkip 是这次回归的语义核心。
//
// 前端选「未分组」时把 group_id 置空，但 `*uint64` 在 JSON 缺失和显式 null
// 两种情况下都是 nil，service 只能判断"没改"。结果是用户永远无法把一个
// 订阅源从分组里移出来 —— 界面上点了没用，也不报错。
//
// GroupUpdate 把"没带这个字段"和"带了这个字段但值为空"分开表达：
// 前者跳过，后者写入 NULL。
func TestBuildFeedUpdatesDistinguishesClearFromSkip(t *testing.T) {
	cases := []struct {
		name    string
		group   GroupUpdate
		wantKey bool
		wantNil bool
		wantVal uint64
	}{
		{name: "字段缺失则不动分组", group: GroupUpdate{}, wantKey: false},
		{name: "显式 null 清空分组", group: GroupUpdate{Set: true, Value: nil}, wantKey: true, wantNil: true},
		{name: "具体值写入分组", group: GroupUpdate{Set: true, Value: uint64Ptr(7)}, wantKey: true, wantVal: 7},
		// Set=false 时即便带了值也不应写入：这是调用方的误用，按"不修改"处理更安全。
		{name: "未声明 Set 时忽略值", group: GroupUpdate{Set: false, Value: uint64Ptr(9)}, wantKey: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updates := buildFeedUpdates("", 0, tc.group)
			value, ok := updates["group_id"]
			if ok != tc.wantKey {
				t.Fatalf("group_id 是否出现在 updates 中 = %v，期望 %v（updates=%v）", ok, tc.wantKey, updates)
			}
			if !ok {
				return
			}
			if tc.wantNil {
				if value != nil {
					t.Errorf("group_id = %v，期望 nil（写入 NULL）", value)
				}
				return
			}
			got, isUint := value.(uint64)
			if !isUint {
				t.Fatalf("group_id 类型 = %T，期望 uint64", value)
			}
			if got != tc.wantVal {
				t.Errorf("group_id = %d，期望 %d", got, tc.wantVal)
			}
		})
	}
}

// TestClearedGroupBecomesSQLNull 确认清除意图真的落成 SQL 里的 NULL。
//
// map 里放了 nil 还不够：GORM 究竟把它写成参数化 NULL、字面量 NULL，
// 还是干脆从 SET 子句剔除，决定了数据库里的分组是否真的被清空。
// 这里用 DryRun 拿到真实语句逐项核对。
func TestClearedGroupBecomesSQLNull(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建 sqlmock 失败: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{DryRun: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("创建 gorm 实例失败: %v", err)
	}

	tx := db.Model(&model.RSSFeed{}).Where("id = ?", 42).
		Updates(buildFeedUpdates("", 0, GroupUpdate{Set: true, Value: nil}))
	if tx.Error != nil {
		t.Fatalf("生成 SQL 失败: %v", tx.Error)
	}

	sql := tx.Statement.SQL.String()
	if !strings.Contains(sql, `"group_id"`) {
		t.Fatalf("SET 子句未包含 group_id，清除不会生效: %s", sql)
	}
	// 只挑出 group_id 对应的占位符位置，避免依赖 updated_at 的位置。
	idx := strings.Index(sql, `"group_id"=$`)
	if idx < 0 {
		t.Fatalf("group_id 未参数化: %s", sql)
	}
	placeholder := sql[idx+len(`"group_id"=$`)]
	paramIndex := int(placeholder - '1')
	if paramIndex < 0 || paramIndex >= len(tx.Statement.Vars) {
		t.Fatalf("占位符 $%c 超出参数列表长度 %d: %s", placeholder, len(tx.Statement.Vars), sql)
	}
	if tx.Statement.Vars[paramIndex] != nil {
		t.Errorf("group_id 参数 = %v，期望 nil（对应 SQL NULL）", tx.Statement.Vars[paramIndex])
	}
}
