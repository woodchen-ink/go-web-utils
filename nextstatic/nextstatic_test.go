package nextstatic

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildExportTree 构造一个模拟的 Next.js export 产物目录
func buildExportTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"index.html":                    "<html><head>__SEO_TITLE__</head><body>home</body></html>",
		"index.txt":                     "rsc:home",
		"robots.txt":                    "User-agent: *",
		"about/index.html":              "<html>about</html>",
		"about/__next._index.txt":       "rsc:about",
		"voddetail/_/index.html":        "<html><head>__SEO_TITLE__</head><body>vod</body></html>",
		"voddetail/_/__next._index.txt": "rsc:vod-template",
		"_next/static/chunks/app.js":    "console.log(1)",
	}
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func newTestHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestNewValidatesRoot(t *testing.T) {
	if _, err := New(Config{Root: "/not/exist/dir"}); err == nil {
		t.Error("不存在的 Root 应报错")
	}
}

func TestServeStaticPages(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// 根首页
	rec := get(h, "/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("根首页异常: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("HTML Content-Type = %q", ct)
	}

	// 静态目录页
	rec = get(h, "/about/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "about") {
		t.Errorf("目录页异常: %d", rec.Code)
	}
}

func TestTrailingSlashRedirect(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	rec := get(h, "/about")
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("无斜杠页面请求应 308, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/about/" {
		t.Errorf("Location = %q", loc)
	}
}

func TestRSCHeaders(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// RSC payload: text/x-component + Vary
	rec := get(h, "/about/__next._index.txt")
	if rec.Code != 200 {
		t.Fatalf("RSC 文件请求失败: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/x-component; charset=utf-8" {
		t.Errorf("RSC Content-Type = %q, 必须为 text/x-component", ct)
	}
	if vary := rec.Header().Get("Vary"); vary != rscVary {
		t.Errorf("RSC Vary = %q", vary)
	}

	// 普通 txt 保持 text/plain 且不带 RSC Vary
	rec = get(h, "/robots.txt")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("robots.txt Content-Type = %q, 应为 text/plain", ct)
	}
	if vary := rec.Header().Get("Vary"); strings.Contains(vary, "RSC") {
		t.Errorf("robots.txt 不应带 RSC Vary, got %q", vary)
	}
}

func TestImmutableCacheForHashedAssets(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root})

	rec := get(h, "/_next/static/chunks/app.js")
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("/_next/static 资源应 immutable, got %q", cc)
	}
}

func TestDynamicRouteFallback(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// 动态路由 HTML 回退占位符目录
	rec := get(h, "/voddetail/123/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "vod") {
		t.Errorf("动态路由回退失败: %d %s", rec.Code, rec.Body.String())
	}

	// 动态路由 RSC 数据文件回退占位符目录
	rec = get(h, "/voddetail/123/__next._index.txt")
	if rec.Code != 200 || rec.Body.String() != "rsc:vod-template" {
		t.Errorf("RSC 占位符回退失败: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/x-component; charset=utf-8" {
		t.Errorf("回退 RSC Content-Type = %q", ct)
	}
}

func TestSEOInject(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{
		Root:          root,
		TrailingSlash: true,
		SEOInject: func(r *http.Request, html []byte) []byte {
			return []byte(strings.ReplaceAll(string(html), "__SEO_TITLE__", "<title>注入标题</title>"))
		},
	})

	rec := get(h, "/voddetail/123/")
	if !strings.Contains(rec.Body.String(), "<title>注入标题</title>") {
		t.Errorf("SEO 注入未生效: %s", rec.Body.String())
	}
}

func TestSPAFallbackToRootIndex(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	rec := get(h, "/no-such-page/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("未匹配页面应回退根首页: %d", rec.Code)
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.URL.Path = "/../../etc/passwd" // 绕过 NewRequest 的规范化, 直接注入原始路径
	h.ServeHTTP(rec, req)
	if rec.Code == 200 && strings.Contains(rec.Body.String(), "root:") {
		t.Error("路径穿越未被拦截")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 应 405, got %d", rec.Code)
	}
}

func TestIsRSCPayload(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/index.txt", true},
		{"/about/__next._index.txt", true},
		{"/about/__next._head.txt", true},
		{"/about/__next._tree.txt", true},
		{"/admin/users/__next.!group.admin.users.__PAGE__.txt", true},
		{"/x/__PAGE__.txt", true},
		{"/robots.txt", false},
		{"/ads.txt", false},
		{"/.well-known/security.txt", false},
		{"/app.js", false},
	}
	for _, tt := range tests {
		if got := IsRSCPayload(tt.path); got != tt.expected {
			t.Errorf("IsRSCPayload(%q) = %v, expected %v", tt.path, got, tt.expected)
		}
	}
}
