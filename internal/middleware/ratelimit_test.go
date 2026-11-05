package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeClock 是可手动推进的时钟。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

// TestRateLimiterBlocksAfterBurstAndRefills 锁住令牌桶的两头：
// 超过 burst 立即拒绝，过了补充周期恢复。只测前一半的话，
// 一个「永远拒绝」的实现也能通过。
func TestRateLimiterBlocksAfterBurstAndRefills(t *testing.T) {
	clk := newClock()
	l := NewRateLimiter(3, time.Minute)
	l.now = clk.Now

	for i := range 3 {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("超过 burst 应拒绝")
	}
	if wait <= 0 || wait > time.Minute {
		t.Fatalf("重试等待 = %v，应在 (0, 1m] 内", wait)
	}

	// 别的键不受影响：限流是按 IP 的，不是全局的。
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("其他 IP 不应被连带限流")
	}

	clk.Advance(20 * time.Second) // 3 次/分钟 → 20 秒补一个
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("补充一个令牌后应放行")
	}
}

// TestRateLimiterSweepsIdleKeys 防的是 map 随来访 IP 无限增长。
func TestRateLimiterSweepsIdleKeys(t *testing.T) {
	clk := newClock()
	l := NewRateLimiter(10, time.Minute)
	l.now = clk.Now

	for i := range 100 {
		l.Allow(fmt.Sprintf("10.0.0.%d", i))
	}
	clk.Advance(2 * time.Minute)
	l.Allow("trigger")

	if n := l.Len(); n != 1 {
		t.Fatalf("空闲键应被回收，剩余 %d", n)
	}
}

// TestRateLimiterCapsKeyCount 防的是在一个回收周期内灌满内存。
//
// 回收只能清掉空闲的键；攻击者在一分钟内换 100 万个源地址时，
// 只能靠上限兜住。
func TestRateLimiterCapsKeyCount(t *testing.T) {
	l := NewRateLimiter(10, time.Minute)
	l.maxKeys = 50
	for i := range 500 {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	if n := l.Len(); n > 50 {
		t.Fatalf("键数 %d 超过上限 50", n)
	}
}

// TestRateLimitByIPReturns429 确认中间件超限时回 429 并带 Retry-After，
// 且不调用后续 handler。
func TestRateLimitByIPReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	reached := 0
	r.POST("/p", RateLimitByIP(NewRateLimiter(2, time.Minute)), func(c *gin.Context) {
		reached++
		c.Status(http.StatusOK)
	})

	codes := make([]int, 0, 3)
	for range 3 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/p", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
		if w.Code == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
			t.Error("429 响应应带 Retry-After")
		}
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("状态码序列 = %v，期望 [200 200 429]", codes)
	}
	if reached != 2 {
		t.Fatalf("handler 被调用 %d 次，期望 2", reached)
	}
}

// TestFailureLimiterLocksAndExpires 锁住失败计数的完整生命周期：
// 达到上限锁定 → 锁定期内保持 → 到期后解锁并清零。
func TestFailureLimiterLocksAndExpires(t *testing.T) {
	clk := newClock()
	l := NewFailureLimiter(3, 15*time.Minute)
	l.now = clk.Now

	for range 2 {
		l.Fail("k")
	}
	if locked, _ := l.Locked("k"); locked {
		t.Fatal("未达上限不应锁定")
	}
	l.Fail("k")
	if locked, left := l.Locked("k"); !locked || left <= 0 {
		t.Fatal("达到上限应锁定")
	}

	clk.Advance(15*time.Minute + time.Second)
	if locked, _ := l.Locked("k"); locked {
		t.Fatal("锁定到期后应解锁")
	}
	// 解锁后计数必须清零，否则下一次失败会立刻重新锁定。
	l.Fail("k")
	if locked, _ := l.Locked("k"); locked {
		t.Fatal("到期后应重新计数")
	}
}

// TestFailureLimiterWindowResetsSparseFailures 确认零散的失败不会无限累计。
//
// 一周输错 5 次密码不该被当成暴力破解。
func TestFailureLimiterWindowResetsSparseFailures(t *testing.T) {
	clk := newClock()
	l := NewFailureLimiter(3, 15*time.Minute)
	l.now = clk.Now

	for range 5 {
		l.Fail("k")
		clk.Advance(time.Hour)
	}
	if locked, _ := l.Locked("k"); locked {
		t.Fatal("窗口外的失败不应累计")
	}
}

// TestFailureLimiterSweepsExpiredKeys 防的是用随机用户名把 map 撑大。
func TestFailureLimiterSweepsExpiredKeys(t *testing.T) {
	clk := newClock()
	l := NewFailureLimiter(5, time.Minute)
	l.now = clk.Now

	for i := range 100 {
		l.Fail(fmt.Sprintf("user%d", i))
	}
	clk.Advance(2 * time.Minute)
	l.Fail("trigger")

	if n := l.Len(); n != 1 {
		t.Fatalf("过期记录应被回收，剩余 %d", n)
	}
}

// TestLimitersAreRaceFree 在 -race 下并发读写，证明计数全程持锁。
func TestLimitersAreRaceFree(t *testing.T) {
	rl := NewRateLimiter(1000, time.Minute)
	fl := NewFailureLimiter(1000, time.Minute)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			key := fmt.Sprintf("k%d", i%4)
			for range 200 {
				rl.Allow(key)
				fl.Fail(key)
				fl.Locked(key)
			}
			fl.Reset(key)
		})
	}
	wg.Wait()
}
