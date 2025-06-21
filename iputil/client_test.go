package iputil

import (
	"net/http"
	"testing"
)

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		expected   string
	}{
		{
			name: "Cloudflare场景",
			headers: map[string]string{
				"CF-Connecting-IP": "203.0.113.1",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.1",
		},
		{
			name: "X-Real-IP场景",
			headers: map[string]string{
				"X-Real-IP": "203.0.113.2",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.2",
		},
		{
			name: "X-Forwarded-For场景",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.3, 192.168.1.1, 10.0.0.1",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.3",
		},
		{
			name: "X-Forwarded-For带空格",
			headers: map[string]string{
				"X-Forwarded-For": " 203.0.113.4 , 192.168.1.1",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.4",
		},
		{
			name:       "直连场景",
			headers:    map[string]string{},
			remoteAddr: "203.0.113.5:8080",
			expected:   "203.0.113.5",
		},
		{
			name:       "直连场景无端口",
			headers:    map[string]string{},
			remoteAddr: "203.0.113.6",
			expected:   "203.0.113.6",
		},
		{
			name: "优先级测试 - Cloudflare优先",
			headers: map[string]string{
				"CF-Connecting-IP": "203.0.113.7",
				"X-Real-IP":        "203.0.113.8",
				"X-Forwarded-For":  "203.0.113.9",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				Header:     make(http.Header),
				RemoteAddr: tt.remoteAddr,
			}

			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			result := GetClientIP(req)
			if result != tt.expected {
				t.Errorf("GetClientIP() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
