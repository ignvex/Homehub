package middleware

import (
	"fmt"
	"testing"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
)

// TestSessionCacheSweepsExpiredEntries 防的是缓存 map 只增不减。
//
// 过期条目只在同一个 token 再次访问时被覆盖；登出不走缓存失效的
// 客户端、换浏览器、cookie 被清掉，都会留下永远不再被读的条目。
func TestSessionCacheSweepsExpiredEntries(t *testing.T) {
	sessionCacheMu.Lock()
	sessionCache = make(map[string]sessionCacheEntry)
	sessionCacheLastSweep = time.Time{}
	sessionCacheMu.Unlock()

	past := time.Now().Add(-time.Minute)
	sessionCacheMu.Lock()
	for i := range 100 {
		sessionCache[fmt.Sprintf("old%d", i)] = sessionCacheEntry{user: &model.User{}, expiresAt: past}
	}
	sessionCacheMu.Unlock()

	setCachedUser("fresh", &model.User{})

	sessionCacheMu.RLock()
	n := len(sessionCache)
	sessionCacheMu.RUnlock()
	if n != 1 {
		t.Fatalf("过期条目应被回收，剩余 %d", n)
	}
}
