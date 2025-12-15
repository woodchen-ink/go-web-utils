package uautil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsBrowserUserAgent(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		want      bool
	}{
		{
			name:      "Chrome浏览器",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			want:      true,
		},
		{
			name:      "Firefox浏览器",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0",
			want:      true,
		},
		{
			name:      "Safari浏览器",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Safari/605.1.15",
			want:      true,
		},
		{
			name:      "Edge浏览器",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 Edg/91.0.864.59",
			want:      true,
		},
		{
			name:      "Opera浏览器",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 OPR/77.0.4054.203",
			want:      true,
		},
		{
			name:      "curl工具",
			userAgent: "curl/7.68.0",
			want:      false,
		},
		{
			name:      "Python requests库",
			userAgent: "python-requests/2.26.0",
			want:      false,
		},
		{
			name:      "wget工具",
			userAgent: "Wget/1.20.3 (linux-gnu)",
			want:      false,
		},
		{
			name:      "Go http client",
			userAgent: "Go-http-client/1.1",
			want:      false,
		},
		{
			name:      "空User-Agent",
			userAgent: "",
			want:      false,
		},
		{
			name:      "Googlebot",
			userAgent: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			want:      false,
		},
		{
			name:      "Scrapy爬虫",
			userAgent: "Scrapy/2.5.0 (+https://scrapy.org)",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBrowserUserAgent(tt.userAgent)
			if got != tt.want {
				t.Errorf("IsBrowserUserAgent() = %v, want %v, UA: %s", got, tt.want, tt.userAgent)
			}
		})
	}
}

func TestIsBrowser(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		want      bool
	}{
		{
			name:      "Chrome请求",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			want:      true,
		},
		{
			name:      "curl请求",
			userAgent: "curl/7.68.0",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)

			got := IsBrowser(req)
			if got != tt.want {
				t.Errorf("IsBrowser() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrowserOnlyMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		userAgent      string
		wantStatusCode int
		customMessage  string
	}{
		{
			name:           "允许Chrome浏览器",
			userAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "拦截curl",
			userAgent:      "curl/7.68.0",
			wantStatusCode: http.StatusForbidden,
		},
		{
			name:           "拦截Python requests",
			userAgent:      "python-requests/2.26.0",
			wantStatusCode: http.StatusForbidden,
		},
		{
			name:           "拦截空UA",
			userAgent:      "",
			wantStatusCode: http.StatusForbidden,
		},
		{
			name:           "自定义拒绝消息",
			userAgent:      "curl/7.68.0",
			wantStatusCode: http.StatusForbidden,
			customMessage:  "仅允许浏览器访问",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			})

			// 应用中间件
			var middleware func(http.Handler) http.Handler
			if tt.customMessage != "" {
				middleware = BrowserOnlyMiddleware(tt.customMessage)
			} else {
				middleware = BrowserOnlyMiddleware()
			}
			wrappedHandler := middleware(handler)

			// 创建测试请求
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)
			rec := httptest.NewRecorder()

			// 执行请求
			wrappedHandler.ServeHTTP(rec, req)

			// 检查状态码
			if rec.Code != tt.wantStatusCode {
				t.Errorf("Status code = %v, want %v", rec.Code, tt.wantStatusCode)
			}

			// 检查自定义消息
			if tt.customMessage != "" && rec.Code == http.StatusForbidden {
				body := rec.Body.String()
				found := false
				for i := 0; i <= len(body)-len(tt.customMessage); i++ {
					if body[i:i+len(tt.customMessage)] == tt.customMessage {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Response body should contain custom message: %s", tt.customMessage)
				}
			}
		})
	}
}

func TestAddCustomBrowserPattern(t *testing.T) {
	// 保存原始patterns
	originalLen := len(browserPatterns)

	// 添加自定义pattern
	remove := AddCustomBrowserPattern("custom-browser/")

	// 验证已添加
	if len(browserPatterns) != originalLen+1 {
		t.Errorf("Pattern not added, len = %d, want %d", len(browserPatterns), originalLen+1)
	}

	// 测试自定义浏览器
	userAgent := "Custom-Browser/1.0"
	if !IsBrowserUserAgent(userAgent) {
		t.Errorf("Custom browser pattern not working")
	}

	// 移除pattern
	remove()

	// 验证已移除
	if len(browserPatterns) != originalLen {
		t.Errorf("Pattern not removed, len = %d, want %d", len(browserPatterns), originalLen)
	}

	// 验证移除后不再匹配
	if IsBrowserUserAgent(userAgent) {
		t.Errorf("Custom browser pattern still working after removal")
	}
}

func TestGetBrowserPatterns(t *testing.T) {
	patterns := GetBrowserPatterns()

	// 验证返回的是副本
	if len(patterns) == 0 {
		t.Error("GetBrowserPatterns returned empty slice")
	}

	// 修改返回的副本不应影响原始数据
	originalLen := len(browserPatterns)
	patterns[0] = "modified"

	if browserPatterns[0] == "modified" {
		t.Error("GetBrowserPatterns should return a copy, not the original slice")
	}

	if len(browserPatterns) != originalLen {
		t.Error("Original slice was modified")
	}
}
