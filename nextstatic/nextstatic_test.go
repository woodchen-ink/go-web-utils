package nextstatic

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildExportTree 构造一个混合 Next 15/16 特征的模拟导出产物目录
func buildExportTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"index.html":                    "<html><head>__SEO_TITLE__</head><body>home</body></html>",
		"index.txt":                     "rsc:home",
		"404.html":                      "<html><body>not found page</body></html>",
		"robots.txt":                    "User-agent: *",
		"sw.js":                         "self.addEventListener('fetch',()=>{})",
		"about/index.html":              "<html>about</html>",
		"about/__next._index.txt":       "rsc:about",
		"voddetail/_/index.html":        "<html><head>__SEO_TITLE__</head><body>vod</body></html>",
		"voddetail/_/__next._index.txt": "rsc:vod-template",
		// Next 16 route group 磁盘布局 (点号请求需还原成此结构)
		"admin/users/__next.!KGNvbnNvbGUp/admin/users/__PAGE__.txt": "rsc:admin-users",
		// CZLConnect 形态: 动态详情导出在父级
		"app/index.html": "<html>app-detail-shell</html>",
		"app/index.txt":  "rsc:app-shell",
		// trailingSlash: false 形态
		"pricing.html":               "<html>pricing</html>",
		"_next/static/chunks/app.js": "console.log(1)",
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

	rec := get(h, "/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("根首页异常: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("HTML Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("HTML 应硬化禁缓存, got %q", cc)
	}

	rec = get(h, "/about/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "about") {
		t.Errorf("目录页异常: %d", rec.Code)
	}
}

func TestDotHTMLForm(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root}) // trailingSlash: false 形态

	rec := get(h, "/pricing")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pricing") {
		t.Errorf("<path>.html 形态未命中: %d", rec.Code)
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

func TestRedirectStripsAbsoluteForm(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// absolute-form 请求携带外部 Host, Location 必须为相对路径
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://evil.example/about", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("应 308, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "evil.example") {
		t.Errorf("Location 不得携带外部 Host: %q", loc)
	}
}

func TestRSCHeaders(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// Next 16 RSC 分片
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

	// Next 15 每目录 index.txt 同样按 RSC 处理
	rec = get(h, "/index.txt")
	if ct := rec.Header().Get("Content-Type"); ct != "text/x-component; charset=utf-8" {
		t.Errorf("Next 15 index.txt Content-Type = %q", ct)
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

func TestRouteGroupDotPathRestore(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// 浏览器点号形态 -> 磁盘目录结构
	rec := get(h, "/admin/users/__next.!KGNvbnNvbGUp.admin.users.__PAGE__.txt")
	if rec.Code != 200 || rec.Body.String() != "rsc:admin-users" {
		t.Errorf("route group 点号路径还原失败: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/x-component; charset=utf-8" {
		t.Errorf("还原后 Content-Type = %q", ct)
	}
}

func TestRestoreRSCDotPath(t *testing.T) {
	tests := []struct {
		in       string
		expected string
	}{
		{
			in:       "/admin/users/__next.!KGNvbnNvbGUp.admin.users.__PAGE__.txt",
			expected: "/admin/users/__next.!KGNvbnNvbGUp/admin/users/__PAGE__.txt",
		},
		{
			in:       "/voddetail/123/__next.voddetail.$d$id.__PAGE__.txt",
			expected: "/voddetail/123/__next.voddetail/$d$id/__PAGE__.txt",
		},
		// 平铺文件名不转换
		{in: "/about/__next._index.txt", expected: ""},
		{in: "/about/__next._tree.txt", expected: ""},
		{in: "/__next.__PAGE__.txt", expected: ""},
		// 非 RSC 不转换
		{in: "/robots.txt", expected: ""},
	}
	for _, tt := range tests {
		if got := restoreRSCDotPath(tt.in); got != tt.expected {
			t.Errorf("restoreRSCDotPath(%q) = %q, expected %q", tt.in, got, tt.expected)
		}
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

func TestServiceWorkerHeaders(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root})

	rec := get(h, "/sw.js")
	if rec.Code != 200 {
		t.Fatalf("sw.js 请求失败: %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("sw.js Cache-Control = %q", cc)
	}
	if swa := rec.Header().Get("Service-Worker-Allowed"); swa != "/" {
		t.Errorf("Service-Worker-Allowed = %q", swa)
	}

	// sw.js 缺失时必须 404, 绝不回退 HTML (防浏览器注册 index.html 为 SW)
	emptyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyRoot, "index.html"), []byte("<html>x</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2 := newTestHandler(t, Config{Root: emptyRoot, SPAFallback: true})
	rec = get(h2, "/sw.js")
	if rec.Code != 404 {
		t.Errorf("缺失的 sw.js 应 404, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<html>") {
		t.Error("缺失的 sw.js 不得回退 HTML")
	}
}

func TestDynamicRouteFallback(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// 动态路由 HTML 回退占位符目录
	rec := get(h, "/voddetail/123/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "vod") {
		t.Errorf("动态路由占位符回退失败: %d %s", rec.Code, rec.Body.String())
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

func TestParentDirFallback(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// 动态详情导出在父级: /app/12345/ -> /app/index.html
	rec := get(h, "/app/12345/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app-detail-shell") {
		t.Errorf("父目录回退失败: %d %s", rec.Code, rec.Body.String())
	}

	// 对应 RSC 数据: /app/12345/index.txt -> /app/index.txt
	rec = get(h, "/app/12345/index.txt")
	if rec.Code != 200 || rec.Body.String() != "rsc:app-shell" {
		t.Errorf("父目录 RSC 回退失败: %d %s", rec.Code, rec.Body.String())
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

func TestNotFoundDefaultsTo404Page(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, TrailingSlash: true})

	// 默认防软 404: 未命中页面返回 404.html + 404 状态码, 不兜底根首页
	rec := get(h, "/no-such-page/")
	if rec.Code != 404 {
		t.Errorf("未命中页面应 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not found page") {
		t.Errorf("应返回 404.html 内容: %s", rec.Body.String())
	}

	// 带扩展名的资源未命中: 硬 404, 无 HTML
	rec = get(h, "/no-such-asset.js")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html>") {
		t.Errorf("资源未命中应硬 404: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSPAFallbackMode(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root, SPAFallback: true})

	rec := get(h, "/no-such-page")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("SPAFallback 应回退根首页 200: %d", rec.Code)
	}
}

func TestAPIPrefixJSON404(t *testing.T) {
	root := buildExportTree(t)
	h := newTestHandler(t, Config{Root: root})

	// 默认 /api/ 前缀返回 JSON 404, 不落页面兜底
	rec := get(h, "/api/not-registered")
	if rec.Code != 404 {
		t.Errorf("API 前缀应 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("API 404 应为 JSON, got %q", ct)
	}
	if strings.Contains(rec.Body.String(), "<html>") {
		t.Error("API 404 不得返回 HTML")
	}

	// 显式空 slice 关闭该行为
	h2 := newTestHandler(t, Config{Root: root, APIPrefixes: []string{}, SPAFallback: true})
	rec = get(h2, "/api/not-registered")
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Errorf("关闭 APIPrefixes 后不应返回 JSON 404")
	}
}

func TestCaseInsensitiveClassification(t *testing.T) {
	// 大小写不敏感文件系统上的变体不得绕过 header 分类
	if !IsRSCPayload("/INDEX.TXT") {
		t.Error("INDEX.TXT 应识别为 RSC")
	}
	rec := httptest.NewRecorder()
	SetHeaders(rec, "/_NEXT/static/chunk.js")
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("大写 /_NEXT/static 应 immutable, got %q", cc)
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

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.URL.Path = "/..\\..\\windows\\win.ini"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("反斜杠路径应 400, got %d", rec.Code)
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
		{"/about/__next._full.txt", true},
		{"/admin/users/__next.!KGNvbnNvbGUp.admin.users.__PAGE__.txt", true},
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
