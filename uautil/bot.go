// Package uautil provides utilities for User-Agent detection and filtering.
package uautil

import (
	"net/http"
	"strings"
)

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
	"slurp",           // Yahoo
	"duckduckbot",     // DuckDuckGo
	"baiduspider",     // Baidu
	"yandexbot",       // Yandex
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
	userAgent := strings.ToLower(r.UserAgent())

	// 空 User-Agent 通常是可疑的
	if userAgent == "" {
		return true
	}

	// 如果允许合法爬虫，先检查是否是合法爬虫
	if allowLegitimate {
		for _, pattern := range legitimateBotPatterns {
			if strings.Contains(userAgent, pattern) {
				return false // 是合法爬虫，不拦截
			}
		}
	}

	// 检查是否匹配常见机器人特征
	for _, pattern := range commonBotPatterns {
		if strings.Contains(userAgent, pattern) {
			return true
		}
	}

	return false
}

// IsBotUserAgent 直接检测 User-Agent 字符串是否为机器人
// allowLegitimate 为 true 时允许合法的搜索引擎爬虫
func IsBotUserAgent(userAgent string, allowLegitimate bool) bool {
	ua := strings.ToLower(userAgent)

	if ua == "" {
		return true
	}

	if allowLegitimate {
		for _, pattern := range legitimateBotPatterns {
			if strings.Contains(ua, pattern) {
				return false
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
// 返回的函数可用于移除该特征
func AddCustomBotPattern(pattern string) func() {
	pattern = strings.ToLower(pattern)
	commonBotPatterns = append(commonBotPatterns, pattern)

	// 返回移除函数
	return func() {
		for i, p := range commonBotPatterns {
			if p == pattern {
				commonBotPatterns = append(commonBotPatterns[:i], commonBotPatterns[i+1:]...)
				return
			}
		}
	}
}

// AddLegitimateBot 添加自定义的合法爬虫特征
func AddLegitimateBot(pattern string) func() {
	pattern = strings.ToLower(pattern)
	legitimateBotPatterns = append(legitimateBotPatterns, pattern)

	return func() {
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
	patterns := make([]string, len(commonBotPatterns))
	copy(patterns, commonBotPatterns)
	return patterns
}

// GetLegitimatePatterns 获取当前的合法爬虫特征列表（副本）
func GetLegitimatePatterns() []string {
	patterns := make([]string, len(legitimateBotPatterns))
	copy(patterns, legitimateBotPatterns)
	return patterns
}
