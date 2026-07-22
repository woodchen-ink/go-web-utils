package iputil

import (
	"net/http"
	"testing"
)

func TestNewTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		cidrs   []string
		wantErr bool
	}{
		{name: "合法CIDR", cidrs: []string{"10.0.0.0/8", "172.16.0.0/12"}, wantErr: false},
		{name: "单个IPv4自动补掩码", cidrs: []string{"1.2.3.4"}, wantErr: false},
		{name: "单个IPv6自动补掩码", cidrs: []string{"2001:db8::1"}, wantErr: false},
		{name: "空条目被跳过", cidrs: []string{"", "10.0.0.0/8", " "}, wantErr: false},
		{name: "非法CIDR报错", cidrs: []string{"10.0.0.0/33"}, wantErr: true},
		{name: "非法IP报错", cidrs: []string{"not-an-ip"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTrustedProxies(tt.cidrs...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTrustedProxies(%v) error = %v, wantErr %v", tt.cidrs, err, tt.wantErr)
			}
		})
	}
}

func TestTrustedProxiesClientIP(t *testing.T) {
	tp, err := NewTrustedProxies("10.0.0.0/8", "192.168.1.1")
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
			name:       "可信代理来源时信任转发头",
			remoteAddr: "10.1.2.3:443",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.50"},
			expected:   "203.0.113.50",
		},
		{
			name:       "可信单IP来源时信任转发头",
			remoteAddr: "192.168.1.1:8080",
			headers:    map[string]string{"X-Real-IP": "203.0.113.51"},
			expected:   "203.0.113.51",
		},
		{
			name:       "不可信来源时忽略伪造头",
			remoteAddr: "203.0.113.99:12345",
			headers:    map[string]string{"CF-Connecting-IP": "1.1.1.1", "X-Forwarded-For": "2.2.2.2"},
			expected:   "203.0.113.99",
		},
		{
			name:       "不可信来源无头时返回直连IP",
			remoteAddr: "203.0.113.100:12345",
			headers:    map[string]string{},
			expected:   "203.0.113.100",
		},
		{
			name:       "可信来源无转发头时回退RemoteAddr",
			remoteAddr: "10.9.9.9:443",
			headers:    map[string]string{},
			expected:   "10.9.9.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{Header: make(http.Header), RemoteAddr: tt.remoteAddr}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if got := tp.ClientIP(req); got != tt.expected {
				t.Errorf("ClientIP() = %v, expected %v", got, tt.expected)
			}
		})
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
