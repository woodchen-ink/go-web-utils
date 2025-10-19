package uautil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsBot(t *testing.T) {
	tests := []struct {
		name            string
		userAgent       string
		allowLegitimate bool
		expected        bool
		description     string
	}{
		// 恶意爬虫测试
		{
			name:            "Python Requests Bot",
			userAgent:       "python-requests/2.28.1",
			allowLegitimate: false,
			expected:        true,
			description:     "应该识别 python-requests 为机器人",
		},
		{
			name:            "Curl Bot",
			userAgent:       "curl/7.68.0",
			allowLegitimate: false,
			expected:        true,
			description:     "应该识别 curl 为机器人",
		},
		{
			name:            "Scrapy Bot",
			userAgent:       "Scrapy/2.5.0 (+https://scrapy.org)",
			allowLegitimate: false,
			expected:        true,
			description:     "应该识别 Scrapy 为机器人",
		},
		{
			name:            "Empty User-Agent",
			userAgent:       "",
			allowLegitimate: false,
			expected:        true,
			description:     "空 User-Agent 应该被识别为机器人",
		},

		// 合法爬虫测试
		{
			name:            "Googlebot - Allow Legitimate",
			userAgent:       "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			allowLegitimate: true,
			expected:        false,
			description:     "允许合法爬虫时，Googlebot 应该被允许",
		},
		{
			name:            "Googlebot - Block All",
			userAgent:       "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			allowLegitimate: false,
			expected:        true,
			description:     "不允许合法爬虫时，Googlebot 应该被拦截",
		},
		{
			name:            "Bingbot - Allow Legitimate",
			userAgent:       "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
			allowLegitimate: true,
			expected:        false,
			description:     "允许合法爬虫时，Bingbot 应该被允许",
		},

		// 正常浏览器测试
		{
			name:            "Chrome Browser",
			userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			allowLegitimate: false,
			expected:        false,
			description:     "Chrome 浏览器应该被允许",
		},
		{
			name:            "Firefox Browser",
			userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0",
			allowLegitimate: false,
			expected:        false,
			description:     "Firefox 浏览器应该被允许",
		},
		{
			name:            "Safari Browser",
			userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Safari/605.1.15",
			allowLegitimate: false,
			expected:        false,
			description:     "Safari 浏览器应该被允许",
		},

		// 其他机器人
		{
			name:            "Java HTTP Client",
			userAgent:       "Java/11.0.11",
			allowLegitimate: false,
			expected:        true,
			description:     "Java HTTP 客户端应该被识别为机器人",
		},
		{
			name:            "Wget",
			userAgent:       "Wget/1.20.3 (linux-gnu)",
			allowLegitimate: false,
			expected:        true,
			description:     "Wget 应该被识别为机器人",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)

			result := IsBot(req, tt.allowLegitimate)
			if result != tt.expected {
				t.Errorf("%s: got %v, want %v - %s", tt.name, result, tt.expected, tt.description)
			}
		})
	}
}

func TestIsBotUserAgent(t *testing.T) {
	tests := []struct {
		name            string
		userAgent       string
		allowLegitimate bool
		expected        bool
	}{
		{
			name:            "Python Requests",
			userAgent:       "python-requests/2.28.1",
			allowLegitimate: false,
			expected:        true,
		},
		{
			name:            "Normal Browser",
			userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/91.0",
			allowLegitimate: false,
			expected:        false,
		},
		{
			name:            "Googlebot Allowed",
			userAgent:       "Googlebot/2.1",
			allowLegitimate: true,
			expected:        false,
		},
		{
			name:            "Googlebot Blocked",
			userAgent:       "Googlebot/2.1",
			allowLegitimate: false,
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsBotUserAgent(tt.userAgent, tt.allowLegitimate)
			if result != tt.expected {
				t.Errorf("IsBotUserAgent(%q, %v) = %v, want %v",
					tt.userAgent, tt.allowLegitimate, result, tt.expected)
			}
		})
	}
}

func TestBlockBotMiddleware(t *testing.T) {
	tests := []struct {
		name            string
		userAgent       string
		allowLegitimate bool
		expectedStatus  int
		expectedBody    string
	}{
		{
			name:            "Block Python Requests",
			userAgent:       "python-requests/2.28.1",
			allowLegitimate: false,
			expectedStatus:  http.StatusForbidden,
			expectedBody:    "Bot access denied",
		},
		{
			name:            "Allow Normal Browser",
			userAgent:       "Mozilla/5.0 Chrome/91.0",
			allowLegitimate: false,
			expectedStatus:  http.StatusOK,
			expectedBody:    "OK",
		},
		{
			name:            "Allow Googlebot",
			userAgent:       "Googlebot/2.1",
			allowLegitimate: true,
			expectedStatus:  http.StatusOK,
			expectedBody:    "OK",
		},
		{
			name:            "Block Googlebot",
			userAgent:       "Googlebot/2.1",
			allowLegitimate: false,
			expectedStatus:  http.StatusForbidden,
			expectedBody:    "Bot access denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试处理器
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			})

			// 应用中间件
			middleware := BlockBotMiddleware(tt.allowLegitimate)
			wrappedHandler := middleware(handler)

			// 创建测试请求
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)
			w := httptest.NewRecorder()

			// 执行请求
			wrappedHandler.ServeHTTP(w, req)

			// 验证结果
			if w.Code != tt.expectedStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.expectedStatus)
			}

			if tt.expectedStatus == http.StatusForbidden {
				if !contains(w.Body.String(), tt.expectedBody) {
					t.Errorf("Body = %q, want to contain %q", w.Body.String(), tt.expectedBody)
				}
			}
		})
	}
}

func TestCustomBotPattern(t *testing.T) {
	// 添加自定义机器人特征
	// 使用完全不在默认列表中的字符串
	remove := AddCustomBotPattern("testcustomagent")
	defer remove()

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "TestCustomAgent/1.0")

	if !IsBot(req, false) {
		t.Error("自定义机器人特征应该被识别")
	}

	// 移除自定义特征
	remove()

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("User-Agent", "TestCustomAgent/1.0")

	if IsBot(req2, false) {
		t.Error("移除后不应该被识别为机器人")
	}
}

func TestAddLegitimateBot(t *testing.T) {
	// 添加自定义合法爬虫
	remove := AddLegitimateBot("friendlybot")
	defer remove()

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "FriendlyBot/1.0")

	// allowLegitimate = true 时应该允许
	if IsBot(req, true) {
		t.Error("自定义合法爬虫在 allowLegitimate=true 时应该被允许")
	}

	// allowLegitimate = false 时应该拦截
	if !IsBot(req, false) {
		t.Error("自定义合法爬虫在 allowLegitimate=false 时应该被拦截")
	}
}

func TestGetPatterns(t *testing.T) {
	botPatterns := GetBotPatterns()
	if len(botPatterns) == 0 {
		t.Error("应该返回机器人特征列表")
	}

	legitimatePatterns := GetLegitimatePatterns()
	if len(legitimatePatterns) == 0 {
		t.Error("应该返回合法爬虫特征列表")
	}

	// 验证返回的是副本（修改不影响原列表）
	originalLen := len(botPatterns)
	botPatterns[0] = "modified"
	newPatterns := GetBotPatterns()
	if newPatterns[0] == "modified" {
		t.Error("GetBotPatterns 应该返回副本，而不是原列表")
	}
	if len(newPatterns) != originalLen {
		t.Error("GetBotPatterns 返回的列表长度不应该改变")
	}
}

func TestCustomMessage(t *testing.T) {
	customMsg := "Access denied for security reasons"
	middleware := BlockBotMiddleware(false, customMsg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "python-requests/2.28.1")
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}

	if !contains(w.Body.String(), customMsg) {
		t.Errorf("Body = %q, want to contain %q", w.Body.String(), customMsg)
	}
}

// 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
