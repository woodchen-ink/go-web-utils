package cookieutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// getCookie 从 recorder 中按名取出 Set-Cookie 结果
func getCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("未找到 Cookie %q", name)
	return nil
}

func TestSetAuthDefaults(t *testing.T) {
	rec := httptest.NewRecorder()
	SetAuth(rec, "session_id", "abc123", Options{})

	c := getCookie(t, rec, "session_id")
	if !c.HttpOnly {
		t.Error("认证 Cookie 必须 HttpOnly")
	}
	if c.Path != "/" {
		t.Errorf("Path 默认应为 /, got %q", c.Path)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite 默认应为 Lax, got %v", c.SameSite)
	}
	if c.MaxAge != 0 {
		t.Errorf("零值 Options 应为会话 Cookie, MaxAge got %d", c.MaxAge)
	}
}

func TestSetAuthWithMaxAgeAndSecure(t *testing.T) {
	rec := httptest.NewRecorder()
	SetAuth(rec, "session_id", "abc", Options{Secure: true, MaxAge: 2 * time.Hour, SameSite: http.SameSiteStrictMode})

	c := getCookie(t, rec, "session_id")
	if !c.Secure {
		t.Error("Secure 未生效")
	}
	if c.MaxAge != 7200 {
		t.Errorf("MaxAge = %d, expected 7200", c.MaxAge)
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, expected Strict", c.SameSite)
	}
}

func TestClear(t *testing.T) {
	rec := httptest.NewRecorder()
	Clear(rec, "session_id", Options{})

	c := getCookie(t, rec, "session_id")
	if c.MaxAge != -1 {
		t.Errorf("Clear 应设 MaxAge -1, got %d", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("Clear 应清空值, got %q", c.Value)
	}
}

func TestNewCSRFToken(t *testing.T) {
	t1, err := NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	t2, _ := NewCSRFToken()
	if t1 == t2 {
		t.Error("两次生成的 token 不应相同")
	}
	if len(t1) < 40 { // 32 字节 base64url 约 43 字符
		t.Errorf("token 长度异常: %d", len(t1))
	}
}

func TestSetCSRFReadableByFrontend(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCSRF(rec, "tok", Options{})

	c := getCookie(t, rec, DefaultCSRFCookie)
	if c.HttpOnly {
		t.Error("CSRF Cookie 不能 HttpOnly, 前端需要读取")
	}
}

func TestValidateCSRF(t *testing.T) {
	tests := []struct {
		name     string
		cookie   string
		header   string
		expected bool
	}{
		{name: "一致时通过", cookie: "tok-1", header: "tok-1", expected: true},
		{name: "不一致拒绝", cookie: "tok-1", header: "tok-2", expected: false},
		{name: "缺请求头拒绝", cookie: "tok-1", header: "", expected: false},
		{name: "缺Cookie拒绝", cookie: "", header: "tok-1", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", nil)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: DefaultCSRFCookie, Value: tt.cookie})
			}
			if tt.header != "" {
				r.Header.Set(DefaultCSRFHeader, tt.header)
			}
			if got := ValidateCSRF(r); got != tt.expected {
				t.Errorf("ValidateCSRF() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
