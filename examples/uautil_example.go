package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/uautil"
)

func main() {
	fmt.Println("=== uautil Package Examples ===\n")

	// 示例 1: 基本的机器人检测
	example1()

	// 示例 2: 使用中间件拦截机器人
	example2()

	// 示例 3: 自定义机器人特征
	example3()

	// 示例 4: 允许合法搜索引擎爬虫
	example4()
}

// 示例 1: 基本的机器人检测
func example1() {
	fmt.Println("--- 示例 1: 基本的机器人检测 ---")

	testUserAgents := []string{
		"python-requests/2.28.1",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/91.0",
		"curl/7.68.0",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"",
	}

	for _, ua := range testUserAgents {
		isBot := uautil.IsBotUserAgent(ua, false)
		displayUA := ua
		if displayUA == "" {
			displayUA = "(空)"
		}
		fmt.Printf("User-Agent: %s\n", displayUA)
		fmt.Printf("是否为机器人: %v\n\n", isBot)
	}
}

// 示例 2: 使用中间件拦截机器人
func example2() {
	fmt.Println("--- 示例 2: 使用中间件拦截机器人 ---")
	fmt.Println("启动服务器在 :8080...")
	fmt.Println("可以使用以下命令测试:")
	fmt.Println("  正常访问: curl -H \"User-Agent: Mozilla/5.0\" http://localhost:8080/")
	fmt.Println("  被拦截: curl http://localhost:8080/")
	fmt.Println()

	// 创建一个简单的处理器
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("欢迎访问！你使用的是正常浏览器。\n"))
	})

	// 应用机器人拦截中间件
	// allowLegitimate=false: 拦截所有机器人（包括搜索引擎）
	middleware := uautil.BlockBotMiddleware(false, "检测到机器人访问，访问被拒绝")
	protectedHandler := middleware(handler)

	// 启动服务器（注释掉避免实际运行）
	// log.Fatal(http.ListenAndServe(":8080", protectedHandler))

	// 为了示例，我们只打印说明
	_ = protectedHandler
	fmt.Println("(示例代码，实际未启动服务器)\n")
}

// 示例 3: 自定义机器人特征
func example3() {
	fmt.Println("--- 示例 3: 自定义机器人特征 ---")

	// 添加自定义机器人特征
	fmt.Println("添加自定义机器人特征: 'mybot'")
	removeFunc := uautil.AddCustomBotPattern("mybot")

	// 测试自定义特征
	testUA := "MyBot/1.0 Custom Scraper"
	isBot := uautil.IsBotUserAgent(testUA, false)
	fmt.Printf("User-Agent: %s\n", testUA)
	fmt.Printf("是否为机器人: %v (应该是 true)\n\n", isBot)

	// 移除自定义特征
	fmt.Println("移除自定义机器人特征")
	removeFunc()

	isBot = uautil.IsBotUserAgent(testUA, false)
	fmt.Printf("移除后，是否为机器人: %v (应该是 false)\n\n", isBot)
}

// 示例 4: 允许合法搜索引擎爬虫
func example4() {
	fmt.Println("--- 示例 4: 允许合法搜索引擎爬虫 ---")
	fmt.Println("启动服务器在 :8081...")
	fmt.Println("此配置允许 Google、Bing 等合法搜索引擎爬虫访问\n")

	// 创建处理器
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("欢迎访问！\n"))
	})

	// allowLegitimate=true: 允许合法搜索引擎爬虫，但拦截恶意爬虫
	middleware := uautil.BlockBotMiddleware(true, "检测到恶意机器人，访问被拒绝")
	protectedHandler := middleware(handler)

	// 测试不同的 User-Agent
	testCases := []struct {
		ua       string
		expected string
	}{
		{"python-requests/2.28.1", "被拦截（恶意爬虫）"},
		{"Mozilla/5.0 (compatible; Googlebot/2.1)", "允许访问（合法爬虫）"},
		{"Mozilla/5.0 (Windows NT 10.0) Chrome/91.0", "允许访问（正常浏览器）"},
		{"curl/7.68.0", "被拦截（工具）"},
	}

	fmt.Println("测试结果:")
	for _, tc := range testCases {
		isBot := uautil.IsBotUserAgent(tc.ua, true)
		status := "允许"
		if isBot {
			status = "拦截"
		}
		fmt.Printf("  %s: %s - %s\n", status, tc.ua, tc.expected)
	}

	_ = protectedHandler
	fmt.Println("\n(示例代码，实际未启动服务器)")
}

// 示例 5: 完整的 HTTP 服务器示例（可选运行）
func runFullServer() {
	fmt.Println("=== 完整服务器示例 ===")

	// 创建路由器
	mux := http.NewServeMux()

	// 路由 1: 完全拦截机器人
	mux.HandleFunc("/strict", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("这是严格模式，所有机器人都被拦截\n"))
	})

	// 路由 2: 允许合法爬虫
	mux.HandleFunc("/seo-friendly", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("这是 SEO 友好模式，允许搜索引擎访问\n"))
	})

	// 路由 3: 不使用拦截
	mux.HandleFunc("/public", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("这是公开路由，所有访问都允许\n"))
	})

	// 为不同路由应用不同的中间件
	// 这里需要更复杂的路由处理，可以使用第三方路由器如 gorilla/mux

	fmt.Println("服务器启动在 :8080")
	fmt.Println("路由:")
	fmt.Println("  /strict - 严格模式")
	fmt.Println("  /seo-friendly - SEO 友好模式")
	fmt.Println("  /public - 公开访问")

	log.Fatal(http.ListenAndServe(":8080", mux))
}
