package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/middleware"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// freshLoginLimiters 让每条用例从零计数开始。
func freshLoginLimiters(t *testing.T) {
	t.Helper()
	prevPair, prevIP := loginPairFailures, loginIPFailures
	loginPairFailures = middleware.NewFailureLimiter(5, 15*time.Minute)
	loginIPFailures = middleware.NewFailureLimiter(20, 15*time.Minute)
	t.Cleanup(func() { loginPairFailures, loginIPFailures = prevPair, prevIP })
}

// expectUnknownUser 让一次登录走到「用户不存在」：先按用户名、再按邮箱查，都没有。
func expectUnknownUser(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`FROM "users" WHERE username`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM "users" WHERE \(email`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func postLogin(username, ip string) int {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"wrong"}`, username)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":50000"
	c.Request = req
	AuthLogin(c)
	return w.Code
}

// TestLoginLockoutIsScopedToSourceIP 防的是「别人把管理员锁在门外」。
//
// 旧实现只按用户名计数：任何人对 admin 输错 5 次，真正的管理员也要
// 等 15 分钟。现在锁定的是「用户名 + IP」组合，攻击者只能锁住自己。
func TestLoginLockoutIsScopedToSourceIP(t *testing.T) {
	freshLoginLimiters(t)
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{Auth: service.NewAuthService(db)})

	const attacker, owner = "203.0.113.1", "198.51.100.2"
	for i := range 5 {
		expectUnknownUser(mock)
		if code := postLogin("admin", attacker); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应为 401，got %d", i+1, code)
		}
	}

	// 锁定后不再查库（sqlmock 没有更多期望，查了会报错并变成 500）。
	if code := postLogin("admin", attacker); code != http.StatusTooManyRequests {
		t.Fatalf("同一 IP 第 6 次应为 429，got %d", code)
	}

	// 另一个 IP 的同名登录照常进入校验。
	expectUnknownUser(mock)
	if code := postLogin("admin", owner); code != http.StatusUnauthorized {
		t.Fatalf("其他 IP 不应被锁定，got %d", code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 期望未满足: %v", err)
	}
}

// TestLoginLockoutPerIPCatchesUsernameSpraying 防的是「每个用户名只试几次」。
//
// 只看组合时，攻击者换着用户名试就永远碰不到 5 次上限。
func TestLoginLockoutPerIPCatchesUsernameSpraying(t *testing.T) {
	freshLoginLimiters(t)
	db, mock, cleanup := testutil.NewMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{Auth: service.NewAuthService(db)})

	const ip = "203.0.113.5"
	for i := range 20 {
		expectUnknownUser(mock)
		if code := postLogin(fmt.Sprintf("user%d", i), ip); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应为 401，got %d", i+1, code)
		}
	}
	if code := postLogin("someone-new", ip); code != http.StatusTooManyRequests {
		t.Fatalf("同一 IP 累计 20 次失败后应为 429，got %d", code)
	}
}

// TestLoginLimiterIsRaceFree 在 -race 下并发登录。
//
// 旧实现在锁外读 att.count，与另一个请求的 a.count++ 构成数据竞争。
func TestLoginLimiterIsRaceFree(t *testing.T) {
	freshLoginLimiters(t)
	db, mock, cleanup := testutil.NewUnorderedMockDB(t)
	defer cleanup()
	swapServices(t, &service.Services{Auth: service.NewAuthService(db)})

	for range 32 {
		expectUnknownUser(mock)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 2 {
				postLogin("admin", fmt.Sprintf("192.0.2.%d", i))
			}
		})
	}
	wg.Wait()
}
