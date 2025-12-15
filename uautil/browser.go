package uautil

import (
	"net/http"
	"strings"
)

// 常见浏览器的 User-Agent 特征
var browserPatterns = []string{
	"mozilla/", // 几乎所有现代浏览器都包含 Mozilla
	"chrome/",
	"safari/",
	"firefox/",
	"edge/",
	"edg/",     // Edge Chromium
	"opera/",
	"opr/",     // Opera Chromium
	"brave/",
	"vivaldi/",
}

// IsBrowser 检测请求是否来自真实浏览器
// 通过检查 User-Agent 中是否包含浏览器特征来判断
func IsBrowser(r *http.Request) bool {
	return IsBrowserUserAgent(r.UserAgent())
}

// IsBrowserUserAgent 直接检测 User-Agent 字符串是否为浏览器
func IsBrowserUserAgent(userAgent string) bool {
	ua := strings.ToLower(userAgent)

	// 空 User-Agent 不是浏览器
	if ua == "" {
		return false
	}

	// 如果匹配到机器人特征,不是浏览器
	for _, pattern := range commonBotPatterns {
		if strings.Contains(ua, pattern) {
			return false
		}
	}

	// 检查是否包含浏览器特征
	for _, pattern := range browserPatterns {
		if strings.Contains(ua, pattern) {
			return true
		}
	}

	return false
}

// BrowserOnlyMiddleware 创建一个中间件,仅允许浏览器访问
// customMessage 是可选的自定义拒绝消息
func BrowserOnlyMiddleware(customMessage ...string) func(http.Handler) http.Handler {
	message := "Browser access only"
	if len(customMessage) > 0 && customMessage[0] != "" {
		message = customMessage[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsBrowser(r) {
				http.Error(w, message, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AddCustomBrowserPattern 添加自定义的浏览器特征
// 返回的函数可用于移除该特征
func AddCustomBrowserPattern(pattern string) func() {
	pattern = strings.ToLower(pattern)
	browserPatterns = append(browserPatterns, pattern)

	// 返回移除函数
	return func() {
		for i, p := range browserPatterns {
			if p == pattern {
				browserPatterns = append(browserPatterns[:i], browserPatterns[i+1:]...)
				return
			}
		}
	}
}

// GetBrowserPatterns 获取当前的浏览器特征列表（副本）
func GetBrowserPatterns() []string {
	patterns := make([]string, len(browserPatterns))
	copy(patterns, browserPatterns)
	return patterns
}
