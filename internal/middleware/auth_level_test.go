package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
)

// runLevelGate 用指定等级的用户请求受保护路由，返回状态码。
func runLevelGate(t *testing.T, userLevel, required int) (int, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	c.Set("user", &model.User{Level: userLevel})

	AuthLevelRequired(required)(c)
	return w.Code, c.IsAborted()
}

// TestAuthLevelRequiredMatrix 覆盖 5 个等级 × 5 个门槛的完整矩阵。
//
// 这类权限矩阵最容易写错，而写错的后果是越权或误拒，两种都很难通过
// 人工点击发现。用表驱动把 25 种组合全测一遍，成本比读代码低得多。
func TestAuthLevelRequiredMatrix(t *testing.T) {
	levels := []int{
		model.LevelAdmin, model.LevelOperator, model.LevelEditor,
		model.LevelMember, model.LevelViewer,
	}

	for _, userLevel := range levels {
		for _, required := range levels {
			code, aborted := runLevelGate(t, userLevel, required)

			// 期望：userLevel <= required 时放行
			wantAllowed := userLevel <= required

			if wantAllowed && aborted {
				t.Errorf("等级 %d 访问需 %d 级的接口被拒（状态码 %d）", userLevel, required, code)
			}
			if !wantAllowed {
				if !aborted {
					t.Errorf("等级 %d 竟然能访问需 %d 级的接口", userLevel, required)
				} else if code != http.StatusForbidden {
					t.Errorf("等级 %d 被拒时状态码 = %d，期望 403", userLevel, code)
				}
			}
		}
	}
}

// TestAuthLevelRequiredRejectsAnonymous 确认无 user 时拒绝。
func TestAuthLevelRequiredRejectsAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)

	AuthLevelRequired(model.LevelViewer)(c)

	if !c.IsAborted() {
		t.Error("匿名请求应被中止（不能假设前面一定挂了 AuthRequired）")
	}
}

// TestAuthLevelRequiredRejectsInvalidUserLevel 确认非法等级 fail-closed。
//
// 数据库默认值、JSON 缺字段、迁移漏列都可能产生 0。
// 如果不拒绝，`0 <= 1` 会让一个没初始化的账号拿到最高权限。
func TestAuthLevelRequiredRejectsInvalidUserLevel(t *testing.T) {
	for _, bad := range []int{0, -1, 6, 99} {
		if _, aborted := runLevelGate(t, bad, model.LevelViewer); !aborted {
			t.Errorf("非法等级 %d 竟然通过了最低门槛", bad)
		}
	}
}

// TestAuthLevelRequiredNormalizesRequired 确认越界门槛被夹紧。
//
// 传 0 应当等价于 1 级（最严），而不是“所有人通过”。
func TestAuthLevelRequiredNormalizesRequired(t *testing.T) {
	// required=0 被夹成 LevelAdmin(1)：5 级用户应被拒
	if _, aborted := runLevelGate(t, model.LevelViewer, 0); !aborted {
		t.Error("required=0 应被夹成 1 级（最严），5 级用户不该通过")
	}
	// required=99 被夹成 LevelViewer(5)：所有合法等级都应通过
	for _, lv := range []int{1, 2, 3, 4, 5} {
		if _, aborted := runLevelGate(t, lv, 99); aborted {
			t.Errorf("required=99 夹成 5 级后，等级 %d 应通过", lv)
		}
	}
}

// TestAuthAdminRequiredIsLevelOne 确认 AuthAdminRequired 等价于 1 级门槛。
func TestAuthAdminRequiredIsLevelOne(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1 级通过
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	c.Set("user", &model.User{Level: model.LevelAdmin})
	AuthAdminRequired()(c)
	if c.IsAborted() {
		t.Error("1 级应通过 AuthAdminRequired")
	}

	// 2 级不通过（2 级不是管理员）
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	c2.Set("user", &model.User{Level: model.LevelOperator})
	AuthAdminRequired()(c2)
	if !c2.IsAborted() {
		t.Error("2 级不应通过 AuthAdminRequired")
	}
}

// TestAuthLevelRequiredRejectsWrongType 确认 context 里塞了非 *model.User
// 时不会 panic。
func TestAuthLevelRequiredRejectsWrongType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	c.Set("user", "not-a-user")

	AuthLevelRequired(model.LevelViewer)(c)

	if !c.IsAborted() {
		t.Error("类型不对时应拒绝而不是放行")
	}
}
