package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/config"
	"github.com/ignvex/HomeHub/internal/service"
	"github.com/ignvex/HomeHub/internal/testutil"
)

// TestPublicBackgroundEndpointsNeverReturnNullAssetIDs 是「登录后首页白屏」的
// 端到端回归：用一份缺 selected_asset_ids（home）和显式 null（login）的
// config.json 走真实的 config.Load，再经 HTTP 读 /webui 与 /background。
// 只要有一处下发 null，前端 .map/.length 就会抛错卸载整棵组件树。
func TestPublicBackgroundEndpointsNeverReturnNullAssetIDs(t *testing.T) {
	body := `{
		"database": {"host": "127.0.0.1", "port": 5432, "user": "u", "dbname": "d"},
		"auth": {"session_secret": "` + strings.Repeat("a", 32) + `"},
		"log": {"level": "error", "format": "json"},
		"webui": {"home": {"background": {"mode": "single"}},
		          "login": {"background": {"selected_asset_ids": null}}}
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	db, _, cleanup := testutil.NewUnorderedMockDB(t)
	defer cleanup()
	svc = &service.Services{Media: service.NewMediaService(db), Background: service.NewBackgroundService()}

	r := gin.New()
	r.GET("/webui", WebUIConfig)
	r.GET("/background", BackgroundGet)

	for _, route := range []string{"/webui", "/background"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s 返回 %d: %s", route, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"selected_asset_ids":null`) {
			t.Fatalf("%s 下发了 null: %s", route, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"selected_asset_ids":[]`) {
			t.Fatalf("%s 应下发 []: %s", route, w.Body.String())
		}
	}
}
