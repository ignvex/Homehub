package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
)

// TestAuthRequiredRejectsInactiveUserFromCache 锁住一条回归：
// 账号被停用后，持有旧 cookie 的请求必须立刻被拒绝。
//
// 停用只改 is_active，会话行本身不带启用状态；而认证中间件有一条
// 5 分钟的内存缓存快路径，命中时根本不查库。若不在缓存路径上校验
// IsActive，被停用的人（包括被降权的管理员）可以继续访问全部接口，
// 直到会话自然过期。
func TestAuthRequiredRejectsInactiveUserFromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const token = "inactive-session-token"
	t.Cleanup(func() { InvalidateSession(token) })

	setCachedUser(token, &model.User{
		BaseModel: model.BaseModel{ID: 42},
		Username:  "disabled",
		Level:     model.LevelAdmin, // 即使是管理员也必须被拦
		IsActive:  false,
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	c.Request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})

	AuthRequired()(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("停用账号状态码 = %d，期望 401", w.Code)
	}
	if !c.IsAborted() {
		t.Error("停用账号的请求必须被中止")
	}
	// 不能把停用用户留在 context 里，否则后续 handler 仍拿到 user。
	if _, ok := c.Get("user"); ok {
		t.Error("停用账号不应被注入 context")
	}
	if got := getCachedUser(token); got != nil {
		t.Error("停用账号的缓存应被清除")
	}
}

// TestAuthRequiredAllowsActiveUserFromCache 确认正常用户不受影响，
// 避免为了安全把所有人一起拦掉。
func TestAuthRequiredAllowsActiveUserFromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const token = "active-session-token"
	t.Cleanup(func() { InvalidateSession(token) })

	setCachedUser(token, &model.User{
		BaseModel: model.BaseModel{ID: 7},
		Username:  "alice",
		Level:     model.LevelViewer,
		IsActive:  true,
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/home", nil)
	c.Request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})

	AuthRequired()(c)

	if c.IsAborted() {
		t.Errorf("正常账号被错误中止，状态码 %d", w.Code)
	}
	u, ok := c.Get("user")
	if !ok {
		t.Fatal("正常账号应被注入 context")
	}
	if got := u.(*model.User); got.ID != 7 {
		t.Errorf("注入的用户 ID = %d, want 7", got.ID)
	}
}

// TestAuthOptionalSkipsInactiveUser 确认可选认证不把停用账号当作已登录。
//
// 否则公开页面会渲染出"已登录"的界面，而每个接口都返回 401。
func TestAuthOptionalSkipsInactiveUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const token = "optional-inactive-token"
	t.Cleanup(func() { InvalidateSession(token) })

	setCachedUser(token, &model.User{
		BaseModel: model.BaseModel{ID: 43},
		Username:  "disabled",
		IsActive:  false,
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})

	AuthOptional()(c)

	if c.IsAborted() {
		t.Error("可选认证不应中止请求")
	}
	if _, ok := c.Get("user"); ok {
		t.Error("停用账号不应被当作已登录注入 context")
	}
}
