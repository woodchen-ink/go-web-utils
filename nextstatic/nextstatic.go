/*
Package nextstatic 提供 Next.js 静态导出产物 (output: 'export') 的 Go 托管能力,
兼容 Next.js 15 与 16 两代导出结构。

由 Go 服务统一提供 /api 与 Next.js 静态页面时, 需要正确处理一组容易踩坑的
细节, 本包把它们收敛为一个开箱即用的 Handler:

  - RSC payload (.txt) 的 Content-Type 必须是 text/x-component; charset=utf-8,
    否则 Next.js 16 客户端路由会回退硬导航, 页面上直接出现一片 RSC 文本;
    Next 15 的每目录 index.txt 同样按 RSC 处理
  - RSC .txt 必须带 Vary: RSC, Next-Router-State-Tree, Next-Router-Prefetch,
    防止 CDN/反代把 HTML 与 RSC payload 串台
  - robots.txt 等普通文本资源保持 text/plain, 与 RSC .txt 明确区分
  - Next 16 route group 的点号 RSC 路径 (如 /admin/users/__next.!<b64>.admin.
    users.__PAGE__.txt) 还原为磁盘目录结构后查找
  - 动态路由回退: /voddetail/123/ 回退占位符目录 /voddetail/_/index.html,
    RSC 数据文件同路回退; 另支持"逐级父目录 index.html"形态的动态详情页
  - trailingSlash: true 时无扩展名页面请求先 308 补斜杠
  - 带 hash 的 /_next/static 资源长期缓存 + immutable; HTML 用硬化的 no-store
    (防 CDN 缓存旧 HTML 引用已被覆盖的 chunk); RSC .txt 短缓存
    (max-age=60, stale-while-revalidate=300); sw.js 强制 no-cache 且缺失时
    绝不回退 HTML (防浏览器把 index.html 当 Service Worker 注册)
  - 未命中处理防软 404: 带扩展名的资源请求直接 404; 页面请求默认返回
    404.html (带 404 状态码), 不默认兜底根首页 —— 兜底首页会让 Next 客户端
    router 在错误 URL 上发起 RSC fetch 造成死循环; 纯 SPA 项目可开
    SPAFallback 恢复"根 index.html + 200"行为
  - 未注册的 API 前缀返回 JSON 404 而非 HTML, 防止接口路径被页面兜底吞掉
  - HTML 响应可通过 SEOInject 钩子在写出前注入 __SEO_*__ 占位符内容

使用示例:

	h, err := nextstatic.New(nextstatic.Config{
		Root:          "./web/out",
		TrailingSlash: true,
		SEOInject: func(r *http.Request, html []byte) []byte {
			return seo.Render(r.URL.Path, html)
		},
	})
	// 挂为兜底路由: 先匹配 /api 与 sitemap 等特殊路由, 其余全部交给 h
	mux.Handle("/", h)

缓存: New 时遍历一次导出目录建立文件索引, 之后路径解析只查内存, 未命中请求
不产生磁盘 stat; HTML 首次读盘后留在内存 (总量上限 HTMLCacheMaxBytes)。
代价是运行期替换导出目录不会自动生效 —— 发布新产物后重启进程或调用
Handler.Reload; 本地开发边构建边看效果时设 DisableCache。
索引区分大小写 (与 Linux 一致), 不展开符号链接目录。

限制: 动态段占位符回退只处理单级动态段 (最后一级目录替换为 _); 多级嵌套
动态段等更复杂映射应在外层路由先行改写。

Gin 集成: 返回值是标准 http.Handler, 用 gin.WrapH 挂到 r.NoRoute 即可;
API 路由与动态 sitemap 等特殊路由正常注册, 优先于 NoRoute 匹配。

完整文档: https://go-web-utils.czl.net/docs/nextstatic
*/
package nextstatic

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// rscVary RSC payload 响应必须携带的 Vary 头, 防止 CDN 把 HTML 与 RSC 缓存串台
const rscVary = "RSC, Next-Router-State-Tree, Next-Router-Prefetch"

// rscCacheControl RSC payload 的短缓存策略; 文件名不带 hash, 发版后最多
// max-age + stale-while-revalidate 的时间内可能拿到旧 payload
const rscCacheControl = "public, max-age=60, stale-while-revalidate=300"

// htmlNoStore HTML 响应的硬化禁缓存头; 普通 no-cache 会被部分 CDN
// (如 Cloudflare 默认规则) 忽略, 导致旧 HTML 引用已被覆盖的 chunk
const htmlNoStore = "no-store, no-cache, must-revalidate, max-age=0"

// Config Handler 配置
type Config struct {
	// Root Next.js export 产物目录 (out/), 必填
	Root string
	// TrailingSlash 与 next.config 的 trailingSlash: true 对齐;
	// 开启后无扩展名页面请求会先 308 补斜杠
	TrailingSlash bool
	// SEOInject 可选的 HTML 注入钩子, 在写出 HTML 前调用 (含动态路由占位模板
	// 与 404 页); 注入内容必须自行完成 HTML 转义。
	// 入参 html 是所有请求共享的缓存, 禁止原地改写其中的字节, 必须返回新切片
	// (bytes.Replace / bytes.ReplaceAll / append 均满足)
	SEOInject func(r *http.Request, html []byte) []byte
	// APIPrefixes 命中这些前缀且未被外层路由处理的请求返回 JSON 404,
	// 不落页面兜底; 零值默认 ["/api/"], 显式传空 slice 可关闭
	APIPrefixes []string
	// SPAFallback 页面未命中时兜底根 index.html 并返回 200 (纯 SPA 形态);
	// 默认 false: 返回 404.html 与 404 状态码, 防软 404 与 RSC fetch 死循环
	SPAFallback bool
	// NotFound 可选的 404 处理器; 设置后未命中请求 (含资源与页面) 全部交给它,
	// 替代默认的 404.html 逻辑
	NotFound http.Handler
	// DisableCache 关闭文件索引与 HTML 缓存, 每次请求读盘; 用于本地开发时
	// 前端反复重新构建的场景, 生产环境保持 false
	DisableCache bool
	// HTMLCacheMaxBytes HTML 内存缓存的总量上限; 零值默认 64MB, 负值关闭
	// HTML 缓存 (文件索引仍生效)。达到上限后新页面每次读盘, 不做淘汰
	HTMLCacheMaxBytes int64
}

// Handler Next.js 静态导出托管处理器, 实现 http.Handler
type Handler struct {
	cfg Config
	// snap 导出目录快照; DisableCache 时恒为 nil
	snap atomic.Pointer[snapshot]
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
	if cfg.APIPrefixes == nil {
		cfg.APIPrefixes = []string{"/api/"}
	}
	h := &Handler{cfg: cfg}
	if err := h.Reload(); err != nil {
		return nil, err
	}
	return h, nil
}

// Reload 重新遍历导出目录, 整体替换文件索引并清空 HTML 缓存; 运行期替换了
// 导出产物后调用。可与请求处理并发调用, 失败时保留旧快照。
// DisableCache 模式下是空操作
func (h *Handler) Reload() error {
	if h.cfg.DisableCache {
		return nil
	}
	s, err := buildSnapshot(h.cfg.Root, h.cfg.HTMLCacheMaxBytes)
	if err != nil {
		return err
	}
	h.snap.Store(s)
	return nil
}

// fullPath 把相对导出根的斜杠路径换成磁盘路径
func (h *Handler) fullPath(fsPath string) string {
	return filepath.Join(h.cfg.Root, filepath.FromSlash(fsPath))
}

// exists 判断导出产物中是否存在该文件 (相对导出根、以 "/" 开头的斜杠路径)
func (h *Handler) exists(fsPath string) bool {
	if s := h.snap.Load(); s != nil {
		_, ok := s.files[fsPath]
		return ok
	}
	return fileExists(h.fullPath(fsPath))
}

// readHTML 读取 HTML 文件内容, 有快照时走内存缓存
func (h *Handler) readHTML(fsPath string) ([]byte, error) {
	if s := h.snap.Load(); s != nil {
		return s.readHTML(fsPath, h.fullPath(fsPath))
	}
	return os.ReadFile(h.fullPath(fsPath))
}

// ServeHTTP 解析顺序: API 前缀 JSON 404 → 直接静态文件 → RSC 点号路径还原 →
// RSC 占位符回退 → trailing slash 规范化 → 目录 index.html → <path>.html →
// 动态路由占位符 HTML → 逐级父目录 index.html → 404 (或 SPA 兜底)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// path.Clean 已消除 ".." 穿越; 额外拒绝反斜杠与 NUL,
	// 防止 Windows 上 filepath 把 "\" 当分隔符导致逃逸
	p := path.Clean("/" + r.URL.Path)
	if strings.ContainsAny(p, "\\\x00") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// 未注册的 API 路径返回 JSON 404, 不落页面兜底
	for _, prefix := range h.cfg.APIPrefixes {
		if prefix != "" && strings.HasPrefix(p, prefix) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"data":{},"msg":"not found"}`))
			return
		}
	}

	if p == "/" {
		h.serveHTML(w, r, "/index.html", http.StatusOK)
		return
	}

	if path.Ext(p) != "" {
		h.serveAsset(w, r, p)
		return
	}
	h.servePage(w, r, p)
}

// serveAsset 处理带扩展名的静态资源请求: 直接文件 → RSC 点号路径还原 →
// RSC 占位符回退; 未命中硬 404, 绝不回退 HTML (防软 404,
// 也防 index.html 被当成 sw.js 注册)
func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request, p string) {
	if h.tryServeFile(w, r, p, p) {
		return
	}

	// Next 16 route group 点号路径还原:
	// /admin/users/__next.!<b64>.admin.users.__PAGE__.txt
	// -> /admin/users/__next.!<b64>/admin/users/__PAGE__.txt
	if restored := restoreRSCDotPath(p); restored != "" {
		if h.tryServeFile(w, r, restored, p) {
			return
		}
	}

	if IsRSCPayload(p) {
		// 动态路由 RSC 数据文件: 真实 ID 目录映射回占位符目录
		if alt := placeholderPath(p); alt != "" {
			if h.tryServeFile(w, r, alt, p) {
				return
			}
			if restored := restoreRSCDotPath(alt); restored != "" {
				if h.tryServeFile(w, r, restored, p) {
					return
				}
			}
		}
		// 父目录形态: /app/12345/index.txt -> /app/index.txt
		if parent := dropLastDir(p); parent != "" {
			if h.tryServeFile(w, r, parent, p) {
				return
			}
		}
	}

	h.notFoundAsset(w, r)
}

// servePage 处理无扩展名的页面请求: trailing slash 规范化、目录 index.html、
// <path>.html、动态路由占位符、逐级父目录回退、404/SPA 兜底
func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, p string) {
	// trailingSlash: true 时先 308 补斜杠, 保证相对资源路径正确;
	// 清空 scheme/host/user, 只下发相对 Location, 防 absolute-form
	// 请求携带的外部 Host 变成开放重定向
	if h.cfg.TrailingSlash && !strings.HasSuffix(r.URL.Path, "/") {
		u := *r.URL
		u.Scheme, u.Opaque, u.User, u.Host = "", "", nil, ""
		u.Path = p + "/"
		http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
		return
	}

	// 目录 index.html
	if h.tryServeHTML(w, r, path.Join(p, "index.html")) {
		return
	}
	// trailingSlash: false 导出形态: /about -> about.html
	if h.tryServeHTML(w, r, p+".html") {
		return
	}
	// 动态路由占位符目录: /voddetail/123/ -> /voddetail/_/index.html
	if alt := placeholderPath(path.Join(p, "index.html")); alt != "" {
		if h.tryServeHTML(w, r, alt) {
			return
		}
	}
	// 逐级父目录回退: /app/12345 -> /app/index.html (动态详情导出在父级的形态);
	// 不含根目录, 根首页兜底由 SPAFallback 显式控制
	for dir := path.Dir(p); dir != "/" && dir != "."; dir = path.Dir(dir) {
		if h.tryServeHTML(w, r, path.Join(dir, "index.html")) {
			return
		}
	}

	h.notFoundPage(w, r)
}

// tryServeFile 尝试按静态文件伺服 fsPath (相对导出根的斜杠路径);
// headerPath 用于响应头分类 (保持为原始请求路径)。命中返回 true
func (h *Handler) tryServeFile(w http.ResponseWriter, r *http.Request, fsPath, headerPath string) bool {
	if !h.exists(fsPath) {
		return false
	}
	SetHeaders(w, headerPath)
	http.ServeFile(w, r, h.fullPath(fsPath))
	return true
}

// tryServeHTML 尝试伺服 HTML 页面 (相对导出根的斜杠路径), 命中返回 true
func (h *Handler) tryServeHTML(w http.ResponseWriter, r *http.Request, fsPath string) bool {
	if !h.exists(fsPath) {
		return false
	}
	h.serveHTML(w, r, fsPath, http.StatusOK)
	return true
}

// serveHTML 输出 HTML 页面 (相对导出根的斜杠路径), 应用 SEOInject 钩子与
// 硬化禁缓存策略
func (h *Handler) serveHTML(w http.ResponseWriter, r *http.Request, fsPath string, status int) {
	body, err := h.readHTML(fsPath)
	if err != nil {
		h.notFoundAsset(w, r)
		return
	}
	if h.cfg.SEOInject != nil {
		body = h.cfg.SEOInject(r, body)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", htmlNoStore)
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// notFoundAsset 资源类未命中: 自定义 NotFound 或纯 404, 不回退 HTML;
// 带 no-store, 避免 CDN (如 Cloudflare) 缓存 404, 部署切换期间新资源还未就绪时
// 被缓存的 404 会在切换完成后继续生效
func (h *Handler) notFoundAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.cfg.NotFound != nil {
		h.cfg.NotFound.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// notFoundPage 页面类未命中: SPAFallback 时回退根 index.html (200);
// 否则按 404.html -> 根 index.html 的顺序以 404 状态输出, 都缺失时纯 404
func (h *Handler) notFoundPage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.NotFound != nil {
		h.cfg.NotFound.ServeHTTP(w, r)
		return
	}

	const rootIndex = "/index.html"
	if h.cfg.SPAFallback && h.exists(rootIndex) {
		h.serveHTML(w, r, rootIndex, http.StatusOK)
		return
	}
	if h.exists("/404.html") {
		h.serveHTML(w, r, "/404.html", http.StatusNotFound)
		return
	}
	if h.exists(rootIndex) {
		h.serveHTML(w, r, rootIndex, http.StatusNotFound)
		return
	}
	http.NotFound(w, r)
}

// IsRSCPayload 判断路径是否为 Next.js export 的 RSC 数据文件。
// 覆盖 Next 15 (每目录 index.txt) 与 Next 16 (__next._index.txt /
// __next._head.txt / __next._tree.txt / __next.__PAGE__.txt /
// route group __next.!<b64>... / 点号形态) 两代命名;
// robots.txt / ads.txt 等普通文本资源不会命中。
// 匹配大小写不敏感, 防大小写不敏感文件系统上被变体绕过
func IsRSCPayload(p string) bool {
	base := strings.ToLower(path.Base(p))
	if !strings.HasSuffix(base, ".txt") {
		return false
	}
	return base == "index.txt" ||
		strings.HasPrefix(base, "__next.") ||
		strings.HasSuffix(base, "__page__.txt")
}

// SetHeaders 按响应路径分类设置 Content-Type、Vary 与缓存头。
// 分类: RSC .txt / sw.js / manifest / robots+sitemap / /_next/static /
// 普通 .txt / 图片字体; 其余交给 http.ServeFile 的默认推断
func SetHeaders(w http.ResponseWriter, urlPath string) {
	lower := strings.ToLower(urlPath)
	switch {
	case IsRSCPayload(lower):
		w.Header().Set("Content-Type", "text/x-component; charset=utf-8")
		w.Header().Set("Vary", rscVary)
		// 短缓存: <Link> 预取很密, 避免每次都回源做条件请求; 不用 immutable
		w.Header().Set("Cache-Control", rscCacheControl)
	case lower == "/sw.js":
		// Service Worker 必须实时更新, 且作用域头允许挂根
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
	case lower == "/manifest.json" || strings.HasSuffix(lower, ".webmanifest"):
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
	case lower == "/robots.txt" || lower == "/sitemap.xml":
		w.Header().Set("Cache-Control", "public, max-age=3600")
	case strings.HasPrefix(lower, "/_next/static/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case strings.HasSuffix(lower, ".txt"):
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	case hasAnySuffix(lower, ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".otf"):
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
}

// restoreRSCDotPath 还原 Next 16 route group 的点号 RSC 路径为磁盘目录结构:
// /admin/users/__next.!<b64>.admin.users.__PAGE__.txt
// -> /admin/users/__next.!<b64>/admin/users/__PAGE__.txt
// __next._index.txt 等平铺文件名 (__next. 后紧跟 _) 不转换, 返回空串
func restoreRSCDotPath(p string) string {
	idx := strings.Index(p, "/__next.")
	if idx < 0 {
		return ""
	}
	dirPart := p[:idx+1]
	afterNext := p[idx+1+len("__next."):]
	if strings.HasPrefix(afterNext, "_") || !strings.HasSuffix(afterNext, ".txt") {
		return ""
	}

	parts := strings.Split(strings.TrimSuffix(afterNext, ".txt"), ".")
	if len(parts) < 2 {
		return ""
	}
	// 首段 (route group 编码或路由段) 保持在 __next. 目录名内,
	// 中间段还原为子目录, 末段加回扩展名作为文件名
	segs := append([]string{"__next." + parts[0]}, parts[1:len(parts)-1]...)
	return dirPart + strings.Join(segs, "/") + "/" + parts[len(parts)-1] + ".txt"
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

// dropLastDir 去掉路径最后一级目录 (保留文件名), 用于"动态详情 RSC 数据
// 落在父目录"的形态; 无可去目录时返回空串。
// 例: /app/12345/index.txt -> /app/index.txt
func dropLastDir(p string) string {
	dir, base := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || dir == "/" {
		return ""
	}
	parent := path.Dir(dir)
	if parent == "/" {
		return "/" + base
	}
	return parent + "/" + base
}

// hasAnySuffix 判断 s 是否以任一后缀结尾
func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
