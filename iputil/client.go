/*
Package iputil 提供了 IP 地址相关的实用工具函数。

这个包主要用于 Web 应用中获取和处理客户端 IP 地址，支持各种代理和 CDN 场景。

主要功能:
  - GetClientIP: 获取客户端真实 IP 地址
  - IsValidIP: 验证 IP 地址格式是否正确
  - IsPrivateIP: 判断是否为私有网络 IP

GetClientIP 函数按以下优先级获取客户端 IP:
 1. CF-Connecting-IP (Cloudflare)
 2. X-Real-IP (Nginx 等代理)
 3. X-Forwarded-For (标准代理链)
 4. RemoteAddr (直连)

使用示例:

	import "github.com/woodchen-ink/go-web-utils/iputil"

	func handler(w http.ResponseWriter, r *http.Request) {
		clientIP := iputil.GetClientIP(r)
		if iputil.IsValidIP(clientIP) && !iputil.IsPrivateIP(clientIP) {
			// 处理来自公网的有效IP
		}
	}
*/
package iputil

import (
	"net"
	"net/http"
	"strings"
)

// GetClientIP 获取客户端真实IP地址
// 支持多种代理场景，按优先级获取真实IP
func GetClientIP(r *http.Request) string {
	// 优先级1: Cloudflare 场景
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip // ✅ 直接获取到真实IP
	}

	// 优先级2: 其他代理场景
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip // ✅ 直接获取到真实IP
	}

	// 优先级3: 标准代理链
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// 取第一个IP（原始客户端IP）
		ips := strings.Split(ip, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0]) // ✅ 获取原始IP
		}
	}

	// 兜底：直连IP（需要去掉端口号）
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // 如果解析失败，返回原始值
	}
	return ip
}
