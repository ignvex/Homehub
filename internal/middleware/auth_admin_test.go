package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
)

// TestAuthAdminRequiredRejectsNonAdmin 确认普通用户被拒绝为 403。
//
// /config 可改写数据库连接与 session_secret：一旦普通用户能调用，
// 就能篡改会话签名密钥、劫持全站登录态。
func TestAuthAdminRequiredRejectsNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	c.Set("user", &model.User{Role: "user"})

	AuthAdminRequired()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("非管理员状态码 = %d，期望 403", w.Code)
	}
	if !c.IsAborted() {
		t.Error("非管理员请求应被中止，不得继续执行 handler")
	}
}

// TestAuthAdminRequiredRejectsAnonymous 确认 context 无 user 时同样拒绝。
//
// 该中间件不能假设前面一定挂了 AuthRequired：单独使用时也必须拒绝，
// 否则漏挂认证中间件就会变成匿名可访问。
func TestAuthAdminRequiredRejectsAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)

	AuthAdminRequired()(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("匿名请求状态码 = %d，期望 403", w.Code)
	}
	if !c.IsAborted() {
		t.Error("匿名请求应被中止")
	}
}

// TestAuthAdminRequiredAllowsAdmin 确认管理员通过且不中止链路。
func TestAuthAdminRequiredAllowsAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	c.Set("user", &model.User{Role: "admin"})

	AuthAdminRequired()(c)

	if c.IsAborted() {
		t.Errorf("管理员不应被中止，状态码 = %d", w.Code)
	}
}

// TestAuthAdminRequiredRejectsEmptyRole 确认 role 为空字符串时不被当作管理员。
func TestAuthAdminRequiredRejectsEmptyRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	c.Set("user", &model.User{Role: ""})

	AuthAdminRequired()(c)

	if !c.IsAborted() {
		t.Error("空 role 不应通过管理员校验")
	}
}
