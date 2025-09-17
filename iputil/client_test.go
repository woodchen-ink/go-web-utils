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
			name: "腾讯云EdgeOne场景",
			headers: map[string]string{
				"EO-Client-IP": "203.0.113.2",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.2",
		},
		{
			name: "阿里云CDN场景",
			headers: map[string]string{
				"Ali-CDN-Real-IP": "203.0.113.3",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.3",
		},
		{
			name: "华为云CDN场景",
			headers: map[string]string{
				"X-HW-Real-IP": "203.0.113.4",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.4",
		},
		{
			name: "百度云CDN场景",
			headers: map[string]string{
				"Baidu-Real-IP": "203.0.113.5",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.5",
		},
		{
			name: "七牛云CDN场景",
			headers: map[string]string{
				"X-Qiniu-CDN-Real-IP": "203.0.113.6",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.6",
		},
		{
			name: "网宿CDN场景",
			headers: map[string]string{
				"Cdn-Real-Ip": "203.0.113.7",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.7",
		},
		{
			name: "Fastly CDN场景",
			headers: map[string]string{
				"Fastly-Client-IP": "203.0.113.8",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.8",
		},
		{
			name: "AWS CloudFront场景",
			headers: map[string]string{
				"CloudFront-Viewer-Address": "203.0.113.9",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.9",
		},
		{
			name: "AWS CloudFront带端口场景",
			headers: map[string]string{
				"CloudFront-Viewer-Address": "203.0.113.10:54321",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.10",
		},
		{
			name: "Azure Front Door场景",
			headers: map[string]string{
				"X-Azure-ClientIP": "203.0.113.11",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.11",
		},
		{
			name: "X-Real-IP场景",
			headers: map[string]string{
				"X-Real-IP": "203.0.113.12",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.12",
		},
		{
			name: "X-Forwarded-For场景",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.13, 192.168.1.1, 10.0.0.1",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.13",
		},
		{
			name: "X-Forwarded-For带空格",
			headers: map[string]string{
				"X-Forwarded-For": " 203.0.113.14 , 192.168.1.1",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.14",
		},
		{
			name:       "直连场景",
			headers:    map[string]string{},
			remoteAddr: "203.0.113.15:8080",
			expected:   "203.0.113.15",
		},
		{
			name:       "直连场景无端口",
			headers:    map[string]string{},
			remoteAddr: "203.0.113.16",
			expected:   "203.0.113.16",
		},
		{
			name: "优先级测试 - Cloudflare优先于EdgeOne",
			headers: map[string]string{
				"CF-Connecting-IP": "203.0.113.17",
				"EO-Client-IP":     "203.0.113.18",
				"X-Real-IP":        "203.0.113.19",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.17",
		},
		{
			name: "优先级测试 - EdgeOne优先于阿里云",
			headers: map[string]string{
				"EO-Client-IP":      "203.0.113.20",
				"Ali-CDN-Real-IP":   "203.0.113.21",
				"X-Forwarded-For":   "203.0.113.22",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.20",
		},
		{
			name: "优先级测试 - 完整CDN优先级链",
			headers: map[string]string{
				"CF-Connecting-IP":           "203.0.113.23",
				"EO-Client-IP":               "203.0.113.24",
				"Ali-CDN-Real-IP":            "203.0.113.25",
				"X-HW-Real-IP":               "203.0.113.26",
				"Baidu-Real-IP":              "203.0.113.27",
				"X-Qiniu-CDN-Real-IP":        "203.0.113.28",
				"Cdn-Real-Ip":                "203.0.113.29",
				"Fastly-Client-IP":           "203.0.113.30",
				"CloudFront-Viewer-Address":  "203.0.113.31",
				"X-Azure-ClientIP":           "203.0.113.32",
				"X-Real-IP":                  "203.0.113.33",
				"X-Forwarded-For":            "203.0.113.34",
			},
			remoteAddr: "192.168.1.1:8080",
			expected:   "203.0.113.23", // Cloudflare应该有最高优先级
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
