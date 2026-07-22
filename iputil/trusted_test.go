package iputil

import (
	"net/http"
	"testing"
)

func TestNewTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		cidrs   []string
		wantErr bool
	}{
		{name: "合法CIDR", header: "CF-Connecting-IP", cidrs: []string{"10.0.0.0/8", "172.16.0.0/12"}, wantErr: false},
		{name: "单个IPv4自动补掩码", header: "X-Real-IP", cidrs: []string{"1.2.3.4"}, wantErr: false},
		{name: "单个IPv6自动补掩码", header: "X-Real-IP", cidrs: []string{"2001:db8::1"}, wantErr: false},
		{name: "空条目被跳过", header: "X-Real-IP", cidrs: []string{"", "10.0.0.0/8", " "}, wantErr: false},
		{name: "可信头缺失报错", header: "", cidrs: []string{"10.0.0.0/8"}, wantErr: true},
		{name: "无有效网段报错", header: "X-Real-IP", cidrs: []string{"", " "}, wantErr: true},
		{name: "空网段列表报错", header: "X-Real-IP", cidrs: []string{}, wantErr: true},
		{name: "非法CIDR报错", header: "X-Real-IP", cidrs: []string{"10.0.0.0/33"}, wantErr: true},
		{name: "非法IP报错", header: "X-Real-IP", cidrs: []string{"not-an-ip"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTrustedProxies(tt.header, tt.cidrs...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTrustedProxies(%q, %v) error = %v, wantErr %v", tt.header, tt.cidrs, err, tt.wantErr)
			}
		})
	}
}

// newRequest 构造带 RemoteAddr 与请求头的测试请求
func newRequest(remoteAddr string, headers map[string]string) *http.Request {
	req := &http.Request{Header: make(http.Header), RemoteAddr: remoteAddr}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestTrustedProxiesVendorHeader(t *testing.T) {
	tp, err := NewTrustedProxies("CF-Connecting-IP", "10.0.0.0/8", "192.168.1.1")
	if err != nil {
		t.Fatalf("NewTrustedProxies: %v", err)
	}

	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		expected   string
	}{
		{
			name:       "可信来源时取声明的可信头",
			remoteAddr: "10.1.2.3:443",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.50"},
			expected:   "203.0.113.50",
		},
		{
			name:       "可信单IP来源同样生效",
			remoteAddr: "192.168.1.1:8080",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.51"},
			expected:   "203.0.113.51",
		},
		{
			name:       "未声明的厂商头不参与解析",
			remoteAddr: "10.1.2.3:443",
			headers:    map[string]string{"EO-Client-IP": "1.1.1.1", "X-Real-IP": "2.2.2.2"},
			expected:   "10.1.2.3", // 只认 CF-Connecting-IP, 其它头视为可伪造
		},
		{
			name:       "不可信来源时忽略一切转发头",
			remoteAddr: "203.0.113.99:12345",
			headers:    map[string]string{"CF-Connecting-IP": "1.1.1.1", "X-Forwarded-For": "2.2.2.2"},
			expected:   "203.0.113.99",
		},
		{
			name:       "可信来源但可信头缺失时回退直连IP",
			remoteAddr: "10.9.9.9:443",
			headers:    map[string]string{},
			expected:   "10.9.9.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tp.ClientIP(newRequest(tt.remoteAddr, tt.headers)); got != tt.expected {
				t.Errorf("ClientIP() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestTrustedProxiesXFF(t *testing.T) {
	tp, err := NewTrustedProxies("X-Forwarded-For", "10.0.0.0/8")
	if err != nil {
		t.Fatalf("NewTrustedProxies: %v", err)
	}

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		expected   string
	}{
		{
			name:       "单跳取唯一条目",
			remoteAddr: "10.1.2.3:443",
			xff:        "203.0.113.50",
			expected:   "203.0.113.50",
		},
		{
			name:       "客户端伪造前缀时取可信代理追加的真实IP",
			remoteAddr: "10.1.2.3:443",
			xff:        "1.1.1.1, 203.0.113.50",
			expected:   "203.0.113.50",
		},
		{
			name:       "多级可信代理链从右往左取第一个不可信IP",
			remoteAddr: "10.1.2.3:443",
			xff:        "203.0.113.60, 10.0.0.5, 10.0.0.6",
			expected:   "203.0.113.60",
		},
		{
			name:       "链上全部可信时返回最左侧IP",
			remoteAddr: "10.1.2.3:443",
			xff:        "10.0.0.7, 10.0.0.8",
			expected:   "10.0.0.7",
		},
		{
			name:       "非法条目被跳过",
			remoteAddr: "10.1.2.3:443",
			xff:        "203.0.113.70, garbage, 10.0.0.5",
			expected:   "203.0.113.70",
		},
		{
			name:       "XFF为空时回退直连IP",
			remoteAddr: "10.1.2.3:443",
			xff:        "",
			expected:   "10.1.2.3",
		},
		{
			name:       "全部条目非法时回退直连IP",
			remoteAddr: "10.1.2.3:443",
			xff:        "garbage, more-garbage",
			expected:   "10.1.2.3",
		},
		{
			name:       "不可信来源时忽略XFF",
			remoteAddr: "203.0.113.99:1",
			xff:        "1.1.1.1",
			expected:   "203.0.113.99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{}
			if tt.xff != "" {
				headers["X-Forwarded-For"] = tt.xff
			}
			if got := tp.ClientIP(newRequest(tt.remoteAddr, headers)); got != tt.expected {
				t.Errorf("ClientIP() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestTrustedProxiesHeaderWithPort(t *testing.T) {
	// CloudFront-Viewer-Address 形态: 头值带端口需被剥离
	tp, err := NewTrustedProxies("CloudFront-Viewer-Address", "10.0.0.0/8")
	if err != nil {
		t.Fatalf("NewTrustedProxies: %v", err)
	}

	req := newRequest("10.1.2.3:443", map[string]string{
		"CloudFront-Viewer-Address": "2a02:cf40:add:4002:91f2:a9b2:e09a:6fc6:41300",
	})
	if got := tp.ClientIP(req); got != "2a02:cf40:add:4002:91f2:a9b2:e09a:6fc6" {
		t.Errorf("ClientIP() = %v, 应剥离端口", got)
	}
}

func TestTrustedProxiesRejectsInvalidHeaderValue(t *testing.T) {
	tp, err := NewTrustedProxies("X-Real-IP", "10.0.0.0/8")
	if err != nil {
		t.Fatalf("NewTrustedProxies: %v", err)
	}

	// 边缘漏配透传的伪造值不是合法 IP 时, 必须回退直连 IP,
	// 不把任意字符串带进安全决策链路
	req := newRequest("10.1.2.3:443", map[string]string{
		"X-Real-IP": "1.2.3.4' OR '1'='1",
	})
	if got := tp.ClientIP(req); got != "10.1.2.3" {
		t.Errorf("非法头值应回退直连 IP, got %v", got)
	}
}

func TestIsIPInCIDRs(t *testing.T) {
	cidrs := []string{"10.0.0.0/8", "2001:db8::/32", "bad-cidr"}

	tests := []struct {
		ip       string
		expected bool
	}{
		{"10.255.255.255", true},
		{"11.0.0.1", false},
		{"2001:db8::1", true},
		{"2001:db9::1", false},
		{"not-an-ip", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsIPInCIDRs(tt.ip, cidrs); got != tt.expected {
			t.Errorf("IsIPInCIDRs(%q) = %v, expected %v", tt.ip, got, tt.expected)
		}
	}
}

func TestIsIPv4IsIPv6(t *testing.T) {
	tests := []struct {
		ip string
		v4 bool
		v6 bool
	}{
		{"192.168.1.1", true, false},
		{"2001:db8::1", false, true},
		{"::ffff:1.2.3.4", true, false}, // IPv4 映射地址按 IPv4 处理
		{"not-an-ip", false, false},
		{"", false, false},
	}

	for _, tt := range tests {
		if got := IsIPv4(tt.ip); got != tt.v4 {
			t.Errorf("IsIPv4(%q) = %v, expected %v", tt.ip, got, tt.v4)
		}
		if got := IsIPv6(tt.ip); got != tt.v6 {
			t.Errorf("IsIPv6(%q) = %v, expected %v", tt.ip, got, tt.v6)
		}
	}
}
