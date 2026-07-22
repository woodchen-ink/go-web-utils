/*
Package nextstatic 提供 Next.js 静态导出产物 (output: 'export') 的 Go 托管能力。

由 Go 服务统一提供 /api 与 Next.js 静态页面时, 需要正确处理一组容易踩坑的细节,
本包把它们收敛为一个开箱即用的 Handler:

  - RSC payload (.txt) 的 Content-Type 必须是 text/x-component; charset=utf-8,
    否则 Next.js 16 客户端路由会回退硬导航, 页面上直接出现一片 RSC 文本
  - RSC .txt 必须带 Vary: RSC, Next-Router-State-Tree, Next-Router-Prefetch,
    防止 CDN/反代把 HTML 与 RSC payload 串台
  - robots.txt 等普通文本资源保持 text/plain, 与 RSC .txt 明确区分
  - 带 hash 的 /_next/static 资源长期缓存 + immutable; HTML 与 RSC 走协商缓存
  - trailingSlash: true 时无扩展名页面请求先 308 补斜杠再查找目录 index.html
  - 动态路由回退: /voddetail/123/ 回退到占位符目录 /voddetail/_/index.html,
    对应的 RSC 数据文件同样从真实 ID 路径映射回占位符目录
  - HTML 响应可通过 SEOInject 钩子在写出前注入 __SEO_*__ 占位符内容

使用示例:

	h, err := nextstatic.New(nextstatic.Config{
		Root:          "./web/out",
		TrailingSlash: true,
		SEOInject: func(r *http.Request, html []byte) []byte {
			return seo.Render(r.URL.Path, html)
		},
	})
	// 挂为兜底路由: 先匹配 /api 与特殊路由, 其余全部交给 h
	mux.Handle("/", h)

route group 深层嵌套的点号 RSC 路径 (如 __next.!group.admin.users.__PAGE__.txt)
按原样与占位符目录两种方式查找; 更复杂的项目自定义映射可在外层路由先行改写。
*/
package nextstatic

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// rscVary RSC payload 响应必须携带的 Vary 头, 防止 CDN 把 HTML 与 RSC 缓存串台
const rscVary = "RSC, Next-Router-State-Tree, Next-Router-Prefetch"

// Config Handler 配置
type Config struct {
	// Root Next.js export 产物目录 (out/), 必填
	Root string
	// TrailingSlash 与 next.config 的 trailingSlash: true 对齐;
	// 开启后无扩展名页面请求会先 308 补斜杠
	TrailingSlash bool
	// SEOInject 可选的 HTML 注入钩子, 在写出 HTML 前调用 (含动态路由占位模板);
	// 注入内容必须自行完成 HTML 转义
	SEOInject func(r *http.Request, html []byte) []byte
	// NotFound 可选的 404 处理器, 默认 http.NotFound
	NotFound http.Handler
}

// Handler Next.js 静态导出托管处理器, 实现 http.Handler
type Handler struct {
	cfg Config
}

// New 创建托管处理器; Root 不存在或不是目录时返回错误
func New(cfg Config) (*Handler, error) {
	info, err := os.Stat(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("nextstatic: root %q: %w", cfg.Root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("nextstatic: root %q is not a directory", cfg.Root)
	}
	return &Handler{cfg: cfg}, nil
}

// ServeHTTP 按以下顺序解析请求:
// 直接静态文件 → RSC 占位符回退 → trailing slash 规范化 →
// 目录 index.html → 动态路由占位符 HTML → 根首页回退 → 404
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p := path.Clean("/" + r.URL.Path)
	if strings.Contains(p, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if p == "/" {
		h.serveHTML(w, r, filepath.Join(h.cfg.Root, "index.html"))
		return
	}

	if path.Ext(p) != "" {
		h.serveAsset(w, r, p)
		return
	}
	h.servePage(w, r, p)
}

// serveAsset 处理带扩展名的静态资源请求, 含 RSC 数据文件的占位符回退
func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request, p string) {
	full := filepath.Join(h.cfg.Root, filepath.FromSlash(p))
	if fileExists(full) {
		SetHeaders(w, p)
		http.ServeFile(w, r, full)
		return
	}

	// RSC 数据文件: 真实 ID 路径映射回占位符目录
	// /voddetail/123/__next._index.txt -> /voddetail/_/__next._index.txt
	if IsRSCPayload(p) {
		if alt := placeholderPath(p); alt != "" {
			altFull := filepath.Join(h.cfg.Root, filepath.FromSlash(alt))
			if fileExists(altFull) {
				SetHeaders(w, p)
				http.ServeFile(w, r, altFull)
				return
			}
		}
	}

	h.notFound(w, r)
}

// servePage 处理无扩展名的页面请求: trailing slash 规范化、目录 index.html、
// 动态路由占位符回退、根首页兜底
func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, p string) {
	// trailingSlash: true 时先 308 补斜杠, 保证相对资源路径正确
	if h.cfg.TrailingSlash && !strings.HasSuffix(r.URL.Path, "/") {
		u := *r.URL
		u.Path = p + "/"
		http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
		return
	}

	// 目录 index.html
	full := filepath.Join(h.cfg.Root, filepath.FromSlash(p), "index.html")
	if fileExists(full) {
		h.serveHTML(w, r, full)
		return
	}

	// 动态路由占位符目录: /voddetail/123/ -> /voddetail/_/index.html
	if alt := placeholderPath(p + "/index.html"); alt != "" {
		altFull := filepath.Join(h.cfg.Root, filepath.FromSlash(alt))
		if fileExists(altFull) {
			h.serveHTML(w, r, altFull)
			return
		}
	}

	// 根首页回退 (前端路由直达)
	rootIndex := filepath.Join(h.cfg.Root, "index.html")
	if fileExists(rootIndex) {
		h.serveHTML(w, r, rootIndex)
		return
	}

	h.notFound(w, r)
}

// serveHTML 输出 HTML 页面, 应用 SEOInject 钩子与协商缓存策略
func (h *Handler) serveHTML(w http.ResponseWriter, r *http.Request, fullPath string) {
	body, err := os.ReadFile(fullPath)
	if err != nil {
		h.notFound(w, r)
		return
	}
	if h.cfg.SEOInject != nil {
		body = h.cfg.SEOInject(r, body)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// notFound 执行自定义或默认 404
func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	if h.cfg.NotFound != nil {
		h.cfg.NotFound.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// IsRSCPayload 判断路径是否为 Next.js export 的 RSC 数据文件。
// 判定依据文件名特征: index.txt、__next. 前缀或 __PAGE__.txt 结尾的 .txt;
// robots.txt / ads.txt 等普通文本资源不会命中
func IsRSCPayload(p string) bool {
	base := path.Base(p)
	if !strings.HasSuffix(base, ".txt") {
		return false
	}
	return base == "index.txt" ||
		strings.HasPrefix(base, "__next.") ||
		strings.HasSuffix(base, "__PAGE__.txt")
}

// SetHeaders 按响应路径分类设置 Content-Type、Vary 与缓存头:
// RSC .txt、带 hash 的 /_next/static 资源、普通 .txt 三类分支处理,
// 其余类型交给 http.ServeFile 的默认推断
func SetHeaders(w http.ResponseWriter, urlPath string) {
	switch {
	case IsRSCPayload(urlPath):
		w.Header().Set("Content-Type", "text/x-component; charset=utf-8")
		w.Header().Set("Vary", rscVary)
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(urlPath, "/_next/static/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case strings.HasSuffix(urlPath, ".txt"):
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
}

// placeholderPath 把路径的最后一级目录替换为动态路由占位符 "_";
// 无可替换目录时返回空串。
// 例: /voddetail/123/index.html -> /voddetail/_/index.html
func placeholderPath(p string) string {
	dir, base := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || dir == "/" {
		return ""
	}
	parent := path.Dir(dir)
	if parent == "/" {
		return "/_/" + base
	}
	return parent + "/_/" + base
}

// fileExists 判断路径存在且为普通文件
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
