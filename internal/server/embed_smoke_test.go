package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ignvex/HomeHub/web"
)

// TestEmbeddedBuildIsServable 用真正嵌入二进制的那份产物（不是测试替身）
// 走一遍：入口能打开，入口引用的每个 /assets/ 文件都能取到。
//
// 防的是「嵌进去的 index.html 指向一份没被嵌进去的 JS」——例如 embed 模式
// 漏了子目录，或构建顺序反了导致嵌入的是上一版产物的一半。
// 只有占位文件（干净检出）时跳过。
func TestEmbeddedBuildIsServable(t *testing.T) {
	useFrontend(t, web.Dist())
	t.Chdir(t.TempDir()) // 证明不依赖工作目录

	r := newTestEngine()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code == http.StatusServiceUnavailable {
		t.Skip("web/dist 未构建，跳过嵌入产物检查")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}

	refs := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(refs) == 0 {
		t.Fatalf("index.html 里没有 /assets/ 引用：%s", w.Body.String())
	}
	for _, m := range refs {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, m[1], nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: 状态码 %d，长度 %d", m[1], rec.Code, rec.Body.Len())
		}
		if strings.HasPrefix(rec.Body.String(), "<!doctype") {
			t.Errorf("%s 返回了 HTML", m[1])
		}
	}
}
