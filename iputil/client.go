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
