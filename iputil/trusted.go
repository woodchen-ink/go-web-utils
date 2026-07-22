package iputil

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// TrustedProxies 表示一组可信代理网段, 用于带信任边界校验的客户端 IP 提取。
// 只有当请求的直连来源 (RemoteAddr) 落在可信网段内时, 才信任代理转发头;
// 否则视为客户端直连, 直接使用 RemoteAddr, 防止请求头伪造。
type TrustedProxies struct {
	nets []*net.IPNet
}

// NewTrustedProxies 由 CIDR 列表构建可信代理集合
// 条目支持 CIDR (如 "10.0.0.0/8") 或单个 IP (自动按 /32、/128 处理);
// 任一条目非法时返回错误, 不静默跳过
func NewTrustedProxies(cidrs ...string) (*TrustedProxies, error) {
	tp := &TrustedProxies{nets: make([]*net.IPNet, 0, len(cidrs))}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		// 单个 IP 自动补全掩码
		if !strings.Contains(c, "/") {
			ip := net.ParseIP(c)
			if ip == nil {
				return nil, fmt.Errorf("iputil: invalid IP %q", c)
			}
			if ip.To4() != nil {
				c += "/32"
			} else {
				c += "/128"
			}
		}
		_, ipNet, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("iputil: invalid CIDR %q: %w", c, err)
		}
		tp.nets = append(tp.nets, ipNet)
	}
	return tp, nil
}

// Contains 判断 IP 是否落在可信网段内; IP 非法时返回 false
func (tp *TrustedProxies) Contains(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, n := range tp.nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// ClientIP 带信任边界校验地获取客户端真实 IP:
// 直连来源在可信网段内时按 GetClientIP 的完整优先级解析代理头,
// 否则忽略所有代理头直接返回直连 IP。
// 适用于鉴权、限流、封禁等安全决策场景; 仅做日志展示用 GetClientIP 即可
func (tp *TrustedProxies) ClientIP(r *http.Request) string {
	remoteIP := remoteAddrIP(r)
	if tp.Contains(remoteIP) {
		return GetClientIP(r)
	}
	return remoteIP
}

// IsIPInCIDRs 判断 IP 是否落在任一 CIDR 网段内 (一次性判断, 每次调用都会解析 cidrs;
// 高频场景请用 NewTrustedProxies 预编译)。IP 或全部网段非法时返回 false
func IsIPInCIDRs(ip string, cidrs []string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			continue
		}
		if ipNet.Contains(parsed) {
			return true
		}
	}
	return false
}

// IsIPv4 判断字符串是否为合法的 IPv4 地址
func IsIPv4(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	return parsed != nil && parsed.To4() != nil
}

// IsIPv6 判断字符串是否为合法的 IPv6 地址 (不含 IPv4 映射形式之外的 IPv4)
func IsIPv6(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	return parsed != nil && parsed.To4() == nil
}

// remoteAddrIP 提取 RemoteAddr 中的 IP 部分, 无端口时原样返回
func remoteAddrIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
