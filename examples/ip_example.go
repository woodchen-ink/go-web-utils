package main

import (
	"fmt"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/ip"
)

func main() {
	// 创建一个简单的 HTTP 服务器来演示 IP 获取功能
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 获取客户端真实IP
		clientIP := ip.GetClientIP(r)

		// 验证IP是否有效
		isValid := ip.IsValidIP(clientIP)

		// 判断是否为私有IP
		isPrivate := ip.IsPrivateIP(clientIP)

		// 输出结果
		fmt.Fprintf(w, "客户端IP信息:\n")
		fmt.Fprintf(w, "IP地址: %s\n", clientIP)
		fmt.Fprintf(w, "是否有效: %t\n", isValid)
		fmt.Fprintf(w, "是否为私有IP: %t\n", isPrivate)
		fmt.Fprintf(w, "\n请求头信息:\n")
		fmt.Fprintf(w, "CF-Connecting-IP: %s\n", r.Header.Get("CF-Connecting-IP"))
		fmt.Fprintf(w, "X-Real-IP: %s\n", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "X-Forwarded-For: %s\n", r.Header.Get("X-Forwarded-For"))
		fmt.Fprintf(w, "RemoteAddr: %s\n", r.RemoteAddr)
	})

	fmt.Println("服务器启动在 :8080")
	fmt.Println("访问 http://localhost:8080 查看IP信息")
	fmt.Println("可以通过设置不同的请求头来测试不同场景:")
	fmt.Println("curl -H 'CF-Connecting-IP: 203.0.113.1' http://localhost:8080")
	fmt.Println("curl -H 'X-Real-IP: 203.0.113.2' http://localhost:8080")
	fmt.Println("curl -H 'X-Forwarded-For: 203.0.113.3, 192.168.1.1' http://localhost:8080")

	http.ListenAndServe(":8080", nil)
}
