// Package uautil provides utilities for User-Agent detection and filtering.
package uautil

import (
	"net/http"
	"strings"
	"sync"
)

// patternsMu 保护本包所有特征列表 (commonBotPatterns / legitimateBotPatterns / browserPatterns)
// 的并发读写；检测函数持读锁，AddXxx 及其返回的移除函数持写锁
var patternsMu sync.RWMutex

// 常见的机器人 User-Agent 特征列表
var commonBotPatterns = []string{
	// 常见爬虫框架和库
	"python-requests",
	"python-urllib",
	"curl",
	"wget",
	"java/",
	"okhttp",
	"go-http-client",
	"apache-httpclient",

	// 恶意爬虫
	"scrapy",
	"selenium",
	"phantomjs",
	"headless",

	// 扫描器
	"nmap",
	"masscan",
	"nikto",
	"sqlmap",
	"nessus",
	"openvas",
	"acunetix",

	// 其他可疑工具
	"bot",
	"crawler",
	"spider",
	"scraper",
}

// 合法的搜索引擎爬虫（通常需要允许）
var legitimateBotPatterns = []string{
	"googlebot",
	"bingbot",
	"slurp",       // Yahoo
	"duckduckbot", // DuckDuckGo
	"baiduspider", // Baidu
	"yandexbot",   // Yandex
	"facebookexternalhit",
	"twitterbot",
	"linkedinbot",
	"slackbot",
	"discordbot",
	"telegrambot",
}

// IsBot 检测请求是否来自机器人
// allowLegitimate 为 true 时允许合法的搜索引擎爬虫
func IsBot(r *http.Request, allowLegitimate bool) bool {
	return IsBotUserAgent(r.UserAgent(), allowLegitimate)
}

// IsBotUserAgent 直接检测 User-Agent 字符串是否为机器人
// allowLegitimate 为 true 时允许合法的搜索引擎爬虫；空 User-Agent 视为机器人
func IsBotUserAgent(userAgent string, allowLegitimate bool) bool {
	ua := strings.ToLower(userAgent)

	if ua == "" {
		return true
	}

	patternsMu.RLock()
	defer patternsMu.RUnlock()

	if allowLegitimate {
		for _, pattern := range legitimateBotPatterns {
			if strings.Contains(ua, pattern) {
				return false // 是合法爬虫，不拦截
			}
		}
	}

	for _, pattern := range commonBotPatterns {
		if strings.Contains(ua, pattern) {
			return true
		}
	}

	return false
}

// BlockBotMiddleware 创建一个中间件来拦截机器人请求
// allowLegitimate 为 true 时允许合法的搜索引擎爬虫
// customMessage 是可选的自定义拒绝消息
func BlockBotMiddleware(allowLegitimate bool, customMessage ...string) func(http.Handler) http.Handler {
	message := "Bot access denied"
	if len(customMessage) > 0 && customMessage[0] != "" {
		message = customMessage[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsBot(r, allowLegitimate) {
				http.Error(w, message, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AddCustomBotPattern 添加自定义的机器人特征
// 返回的函数可用于移除该特征；添加与移除均可与检测函数并发调用
func AddCustomBotPattern(pattern string) func() {
	pattern = strings.ToLower(pattern)

	patternsMu.Lock()
	commonBotPatterns = append(commonBotPatterns, pattern)
	patternsMu.Unlock()

	// 返回移除函数
	return func() {
		patternsMu.Lock()
		defer patternsMu.Unlock()
		for i, p := range commonBotPatterns {
			if p == pattern {
				commonBotPatterns = append(commonBotPatterns[:i], commonBotPatterns[i+1:]...)
				return
			}
		}
	}
}

// AddLegitimateBot 添加自定义的合法爬虫特征
// 返回的函数可用于移除该特征；添加与移除均可与检测函数并发调用
func AddLegitimateBot(pattern string) func() {
	pattern = strings.ToLower(pattern)

	patternsMu.Lock()
	legitimateBotPatterns = append(legitimateBotPatterns, pattern)
	patternsMu.Unlock()

	return func() {
		patternsMu.Lock()
		defer patternsMu.Unlock()
		for i, p := range legitimateBotPatterns {
			if p == pattern {
				legitimateBotPatterns = append(legitimateBotPatterns[:i], legitimateBotPatterns[i+1:]...)
				return
			}
		}
	}
}

// GetBotPatterns 获取当前的机器人特征列表（副本）
func GetBotPatterns() []string {
	patternsMu.RLock()
	defer patternsMu.RUnlock()
	patterns := make([]string, len(commonBotPatterns))
	copy(patterns, commonBotPatterns)
	return patterns
}

// GetLegitimatePatterns 获取当前的合法爬虫特征列表（副本）
func GetLegitimatePatterns() []string {
	patternsMu.RLock()
	defer patternsMu.RUnlock()
	patterns := make([]string, len(legitimateBotPatterns))
	copy(patterns, legitimateBotPatterns)
	return patterns
}
