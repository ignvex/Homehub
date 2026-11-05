package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================================
// 内存限流
// ============================================================
//
// 两种工具，都只放在进程内存里（单实例部署，不需要外部存储）：
//
//   - RateLimiter：令牌桶，限制「单位时间内的请求次数」，用于匿名写接口。
//   - FailureLimiter：失败计数，限制「连续失败次数」，用于登录。
//
// 两者都会定期回收空闲条目，且有条目数上限：键来自客户端（IP、用户名），
// 不设上限时攻击者换着键请求就能让 map 无限增长。

// defaultMaxKeys 是限流器默认的最大跟踪键数。
const defaultMaxKeys = 10000

// tooManyRequestsCode 与 handler.CodeTooManyRequests 一致。
// middleware 不能 import handler（会成环），改动时两处同步。
const tooManyRequestsCode = 10009

// bucket 是一个令牌桶的状态。
type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter 是按键的令牌桶限流器，并发安全。
//
// 每个键的桶容量为 burst，每 per 时间补满 burst 个令牌，即平均速率
// burst/per。空闲到桶已补满的条目没有任何信息量，回收时直接删掉。
type RateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	burst     float64
	rate      float64 // 每秒补充的令牌数
	maxKeys   int
	lastSweep time.Time
	now       func() time.Time
}

// NewRateLimiter 创建每 per 时间最多 burst 次请求的限流器。
func NewRateLimiter(burst int, per time.Duration) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	if per <= 0 {
		per = time.Minute
	}
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		burst:   float64(burst),
		rate:    float64(burst) / per.Seconds(),
		maxKeys: defaultMaxKeys,
		now:     time.Now,
	}
}

// fullAfter 返回一个空桶补满所需的时间，即条目可被回收的空闲时长。
func (l *RateLimiter) fullAfter() time.Duration {
	return time.Duration(l.burst / l.rate * float64(time.Second))
}

// Allow 消耗 key 的一个令牌；令牌不足时返回 false 与建议的重试等待时间。
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.maybeSweepLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		evictOneIfFull(l.buckets, l.maxKeys)
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.tokens += now.Sub(b.last).Seconds() * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		return false, wait
	}
	b.tokens--
	return true, 0
}

// maybeSweepLocked 每隔一个补满周期回收一次已补满的桶。调用方持有 mu。
//
// 惰性回收而不是后台 goroutine：不需要接入关闭流程，也不会在没有
// 请求时空转。
func (l *RateLimiter) maybeSweepLocked(now time.Time) {
	idle := l.fullAfter()
	if now.Sub(l.lastSweep) < idle {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) >= idle {
			delete(l.buckets, k)
		}
	}
}

// Len 返回当前跟踪的键数（测试与诊断用）。
func (l *RateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// evictOneIfFull 在 m 达到上限时随机淘汰一个条目。
//
// Go 的 map 遍历顺序是随机的，取第一个即随机淘汰。被淘汰的键相当于
// 重新开始计数（对它而言是 fail open）；相比拒绝所有新键（让攻击者
// 灌满 map 就能挡住所有正常用户），这是更可接受的失败方向。
func evictOneIfFull[V any](m map[string]V, maxKeys int) {
	if len(m) < maxKeys {
		return
	}
	for k := range m {
		delete(m, k)
		return
	}
}

// RateLimitByIP 返回按客户端 IP 限流的中间件，超限时回 429。
//
// IP 取 c.ClientIP()：反向代理部署时依赖 Gin 的可信代理配置解析
// X-Forwarded-For。
func RateLimitByIP(l *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, wait := l.Allow(c.ClientIP())
		if !ok {
			secs := int(wait.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code": tooManyRequestsCode, "message": "请求过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
}

// ============================================================
// 失败计数
// ============================================================

// failureRecord 是一个键的失败状态。
type failureRecord struct {
	count       int
	first       time.Time // 本窗口第一次失败的时间
	lockedUntil time.Time
}

// FailureLimiter 按键统计失败次数，达到上限后锁定一段时间，并发安全。
//
// 窗口与锁定时长相同：window 内累计 max 次失败即锁定 window。
// 窗口过期（且未锁定）后计数从零开始。
type FailureLimiter struct {
	mu        sync.Mutex
	records   map[string]*failureRecord
	max       int
	window    time.Duration
	maxKeys   int
	lastSweep time.Time
	now       func() time.Time
}

// NewFailureLimiter 创建 window 内最多 max 次失败的计数器。
func NewFailureLimiter(max int, window time.Duration) *FailureLimiter {
	if max < 1 {
		max = 1
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &FailureLimiter{
		records: make(map[string]*failureRecord),
		max:     max,
		window:  window,
		maxKeys: defaultMaxKeys,
		now:     time.Now,
	}
}

// expiredLocked 判断记录是否已无效（窗口与锁定都已过去）。
func (l *FailureLimiter) expiredLocked(r *failureRecord, now time.Time) bool {
	return now.After(r.lockedUntil) && now.Sub(r.first) >= l.window
}

// Locked 返回 key 当前是否处于锁定状态，以及剩余锁定时间。
func (l *FailureLimiter) Locked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	r, ok := l.records[key]
	if !ok {
		return false, 0
	}
	if now.Before(r.lockedUntil) {
		return true, r.lockedUntil.Sub(now)
	}
	if l.expiredLocked(r, now) {
		delete(l.records, key)
	}
	return false, 0
}

// Fail 记录 key 的一次失败；达到上限时开始锁定。
func (l *FailureLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.maybeSweepLocked(now)

	r, ok := l.records[key]
	if ok && l.expiredLocked(r, now) {
		ok = false
	}
	if !ok {
		evictOneIfFull(l.records, l.maxKeys)
		r = &failureRecord{first: now}
		l.records[key] = r
	}
	r.count++
	if r.count >= l.max && !now.Before(r.lockedUntil) {
		r.lockedUntil = now.Add(l.window)
	}
}

// Reset 清除 key 的失败记录（例如登录成功后）。
func (l *FailureLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.records, key)
	l.mu.Unlock()
}

// maybeSweepLocked 每个窗口回收一次已过期的记录。调用方持有 mu。
func (l *FailureLimiter) maybeSweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for k, r := range l.records {
		if l.expiredLocked(r, now) {
			delete(l.records, k)
		}
	}
}

// Len 返回当前跟踪的键数（测试与诊断用）。
func (l *FailureLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.records)
}
