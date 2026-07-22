/*
Package iputil 提供了 IP 地址相关的实用工具函数。

这个包主要用于 Web 应用中获取和处理客户端 IP 地址，支持各种代理和 CDN 场景。

主要功能:
  - GetClientIP: 获取客户端真实 IP 地址
  - IsValidIP: 验证 IP 地址格式是否正确
  - IsPrivateIP: 判断是否为私有网络 IP

GetClientIP 函数按以下优先级获取客户端 IP:
 1. CF-Connecting-IP (Cloudflare)
 2. EO-Client-IP (腾讯云 EdgeOne)
 3. Ali-CDN-Real-IP (阿里云 CDN)
 4. X-HW-Real-IP (华为云 CDN)
 5. Baidu-Real-IP (百度云 CDN)
 6. X-Qiniu-CDN-Real-IP (七牛云 CDN)
 7. Cdn-Real-Ip (网宿 CDN)
 8. Fastly-Client-IP (Fastly CDN)
 9. CloudFront-Viewer-Address (AWS CloudFront)
 10. X-Azure-ClientIP (Azure Front Door)
 11. X-Real-IP (Nginx 等通用代理)
 12. X-Forwarded-For (标准代理链)
 13. RemoteAddr (直连)

支持的 CDN 和代理:
  - Cloudflare: CF-Connecting-IP
  - 腾讯云 EdgeOne: EO-Client-IP
  - 阿里云 CDN: Ali-CDN-Real-IP
  - 华为云 CDN: X-HW-Real-IP
  - 百度云 CDN: Baidu-Real-IP
  - 七牛云 CDN: X-Qiniu-CDN-Real-IP
  - 网宿 CDN: Cdn-Real-Ip
  - Fastly CDN: Fastly-Client-IP
  - AWS CloudFront: CloudFront-Viewer-Address
  - Azure Front Door: X-Azure-ClientIP
  - 通用代理: X-Real-IP, X-Forwarded-For

使用示例:

	import "github.com/woodchen-ink/go-web-utils/iputil"

	func handler(w http.ResponseWriter, r *http.Request) {
		clientIP := iputil.GetClientIP(r)
		if iputil.IsValidIP(clientIP) && !iputil.IsPrivateIP(clientIP) {
			// 处理来自公网的有效IP
		}
	}

安全提示 (信任边界):

以上所有请求头都可以被直连客户端伪造。GetClientIP 的结果用于日志记录、
数据展示是安全的; 但用于鉴权、限流、封禁等安全决策时, 服务必须部署在
可信代理/CDN 之后, 并由代理层覆盖 (而非透传) 这些请求头, 否则攻击者
可以通过伪造请求头冒充任意 IP。

完整文档: https://go-web-utils.czl.net/docs/iputil
*/
package iputil

import (
	"net"
	"net/http"
	"strings"
)

// GetClientIP 获取客户端真实IP地址
// 支持多种CDN和代理场景，按优先级获取真实IP
//
// 安全提示: 所有请求头均可被直连客户端伪造, 结果用于安全决策 (鉴权/限流/封禁)
// 时必须确保服务部署在可信代理之后且代理层会覆盖这些头, 详见包文档
func GetClientIP(r *http.Request) string {
	// 优先级1: Cloudflare
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}

	// 优先级2: 腾讯云EdgeOne
	if ip := r.Header.Get("EO-Client-IP"); ip != "" {
		return ip
	}

	// 优先级3: 阿里云CDN
	if ip := r.Header.Get("Ali-CDN-Real-IP"); ip != "" {
		return ip
	}

	// 优先级4: 华为云CDN
	if ip := r.Header.Get("X-HW-Real-IP"); ip != "" {
		return ip
	}

	// 优先级5: 百度云CDN
	if ip := r.Header.Get("Baidu-Real-IP"); ip != "" {
		return ip
	}

	// 优先级6: 七牛云CDN
	if ip := r.Header.Get("X-Qiniu-CDN-Real-IP"); ip != "" {
		return ip
	}

	// 优先级7: 网宿CDN
	if ip := r.Header.Get("Cdn-Real-Ip"); ip != "" {
		return ip
	}

	// 优先级8: Fastly CDN
	if ip := r.Header.Get("Fastly-Client-IP"); ip != "" {
		return ip
	}

	// 优先级9: AWS CloudFront
	if ip := r.Header.Get("CloudFront-Viewer-Address"); ip != "" {
		return stripViewerAddressPort(ip)
	}

	// 优先级10: Azure Front Door
	if ip := r.Header.Get("X-Azure-ClientIP"); ip != "" {
		return ip
	}

	// 优先级11: 通用真实IP头
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// 优先级12: 标准代理链
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// 取第一个IP（原始客户端IP）
		ips := strings.Split(ip, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// 兜底：直连IP（需要去掉端口号）
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // 如果解析失败，返回原始值
	}
	return ip
}

// stripViewerAddressPort 去掉 CloudFront-Viewer-Address 中附带的端口号
// 该头格式为 "IP:port"，其中 IPv6 不带方括号（如 2a02:cf40::1:41300），
// net.SplitHostPort 无法直接解析，需按最后一个冒号切分后校验 IP 合法性
func stripViewerAddressPort(addr string) string {
	// IPv4:port 或 [IPv6]:port
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	// 无方括号的 IPv6:port
	if i := strings.LastIndex(addr, ":"); i > 0 {
		if host := addr[:i]; net.ParseIP(host) != nil {
			return host
		}
	}
	return addr
}
