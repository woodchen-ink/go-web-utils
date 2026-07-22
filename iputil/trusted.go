package iputil

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// TrustedProxies 表示"可信代理网段 + 该边缘设置的客户端 IP 头"的组合,
// 用于带信任边界校验的客户端 IP 提取, 供鉴权、限流、封禁等安全决策场景使用
// (仅做日志展示用 GetClientIP 即可)。
//
// 构造时必须显式声明部署边缘会强制覆盖的客户端 IP 头 (如 Cloudflare 的
// CF-Connecting-IP、腾讯云 EdgeOne 的 EO-Client-IP、自建 nginx 明确 set 的
// X-Real-IP)。不做"来源可信就信任所有厂商头"的宽松模式 —— 代理/CDN 会透传
// 客户端伪造的其它厂商头, 例如服务挂在 EdgeOne 后时, 客户端伪造的
// CF-Connecting-IP 会原样到达服务端。
type TrustedProxies struct {
	nets []*net.IPNet
	// header 部署边缘强制覆盖的客户端 IP 头; 为 X-Forwarded-For 时
	// 按"从右往左取第一个不可信 IP"算法解析
	header string
	// headerIsXFF 预判定 header 是否为 X-Forwarded-For, 避免每请求比较
	headerIsXFF bool
}

// NewTrustedProxies 构建可信代理集合。
// trustedHeader 为部署边缘会强制覆盖的客户端 IP 头, 不能为空;
// cidrs 支持 CIDR (如 "10.0.0.0/8") 或单个 IP (自动按 /32、/128 处理),
// 任一条目非法时返回错误, 不静默跳过
func NewTrustedProxies(trustedHeader string, cidrs ...string) (*TrustedProxies, error) {
	trustedHeader = strings.TrimSpace(trustedHeader)
	if trustedHeader == "" {
		return nil, fmt.Errorf("iputil: trustedHeader is required (e.g. \"CF-Connecting-IP\", \"X-Forwarded-For\")")
	}

	tp := &TrustedProxies{
		nets:        make([]*net.IPNet, 0, len(cidrs)),
		header:      trustedHeader,
		headerIsXFF: strings.EqualFold(trustedHeader, "X-Forwarded-For"),
	}
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
	if len(tp.nets) == 0 {
		// 空网段集合会让可信头永远不生效, 是"以为配置了却没配置"的静默陷阱
		return nil, fmt.Errorf("iputil: at least one trusted CIDR is required")
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
//   - 直连来源不在可信网段: 忽略一切转发头, 返回直连 IP
//   - 可信头为 X-Forwarded-For: 从右往左跳过可信网段条目, 返回第一个
//     不可信的合法 IP; 链上全部可信时返回最左侧合法 IP (内部设施互调),
//     无合法条目时返回直连 IP
//   - 其它可信头: 返回该头的值 (自动去除附带端口), 缺失时返回直连 IP
func (tp *TrustedProxies) ClientIP(r *http.Request) string {
	remoteIP := remoteAddrIP(r)
	if !tp.Contains(remoteIP) {
		return remoteIP
	}

	if tp.headerIsXFF {
		return tp.clientIPFromXFF(r, remoteIP)
	}

	if v := strings.TrimSpace(r.Header.Get(tp.header)); v != "" {
		// 头值必须是合法 IP; 边缘漏配导致客户端伪造值透传时,
		// 不把任意字符串放进安全决策链路
		if host := stripViewerAddressPort(v); net.ParseIP(host) != nil {
			return host
		}
	}
	return remoteIP
}

// clientIPFromXFF 按"从右往左取第一个不可信 IP"算法解析 X-Forwarded-For;
// 右侧条目由可信代理逐跳追加, 不可被客户端伪造
func (tp *TrustedProxies) clientIPFromXFF(r *http.Request, remoteIP string) string {
	// 合并全部 X-Forwarded-For 头为一条链
	var chain []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(h, ",") {
			if p := strings.TrimSpace(part); p != "" {
				chain = append(chain, p)
			}
		}
	}

	leftmostValid := ""
	for i := len(chain) - 1; i >= 0; i-- {
		if net.ParseIP(chain[i]) == nil {
			continue // 非法条目跳过, 不作为结果
		}
		leftmostValid = chain[i]
		if !tp.Contains(chain[i]) {
			return chain[i]
		}
	}
	if leftmostValid != "" {
		return leftmostValid
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
