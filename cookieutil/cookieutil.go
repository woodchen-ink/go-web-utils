/*
Package cookieutil 提供带安全默认值的认证 Cookie 与 CSRF 防护工具。

安全基线 (与 Cookie 登录认证配套):
  - 认证 Cookie 强制 HttpOnly, 前端脚本不可读, 防 XSS 窃取
  - 生产环境应设置 Secure = true, 仅走 HTTPS
  - SameSite 默认 Lax, 跨站场景显式指定
  - CSRF 采用 double-submit 方案: 随机 token 同时写入可读 Cookie 与
    请求头, 服务端恒定时间比较两者是否一致

使用示例:

	opts := cookieutil.Options{Secure: true, MaxAge: 7 * 24 * time.Hour}
	cookieutil.SetAuth(w, "session_id", sid, opts)

	token, _ := cookieutil.NewCSRFToken()
	cookieutil.SetCSRF(w, token, opts)

	// 校验写操作请求
	if !cookieutil.ValidateCSRF(r) {
		resputil.FailStatus(w, 403, 40300, "CSRF 校验失败")
		return
	}

完整文档: https://go-web-utils.czl.net/docs/cookieutil
*/
package cookieutil

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// DefaultCSRFCookie CSRF token 的默认 Cookie 名
const DefaultCSRFCookie = "csrf_token"

// DefaultCSRFHeader CSRF token 的默认请求头名
const DefaultCSRFHeader = "X-CSRF-Token"

// Options Cookie 下发选项; 零值即 Path=/、SameSite=Lax、会话级有效期
type Options struct {
	Domain   string
	Path     string        // 为空时默认 "/"
	Secure   bool          // 生产环境必须为 true
	SameSite http.SameSite // 零值时默认 http.SameSiteLaxMode
	MaxAge   time.Duration // <=0 表示会话 Cookie (随浏览器关闭失效)
}

// normalize 补全 Options 的默认值
func (o Options) normalize() Options {
	if o.Path == "" {
		o.Path = "/"
	}
	if o.SameSite == 0 {
		o.SameSite = http.SameSiteLaxMode
	}
	return o
}

// SetAuth 下发认证 Cookie, 强制 HttpOnly (调用方无法关闭),
// 用于 session id / 登录凭证等敏感值
func SetAuth(w http.ResponseWriter, name, value string, opts Options) {
	o := opts.normalize()
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Domain:   o.Domain,
		Path:     o.Path,
		Secure:   o.Secure,
		SameSite: o.SameSite,
		HttpOnly: true,
	}
	if o.MaxAge > 0 {
		c.MaxAge = int(o.MaxAge / time.Second)
		c.Expires = time.Now().Add(o.MaxAge)
	}
	http.SetCookie(w, c)
}

// Clear 清除指定 Cookie (MaxAge -1); Domain/Path 必须与下发时一致才能生效
func Clear(w http.ResponseWriter, name string, opts Options) {
	o := opts.normalize()
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Domain:   o.Domain,
		Path:     o.Path,
		Secure:   o.Secure,
		SameSite: o.SameSite,
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// NewCSRFToken 生成 32 字节加密随机 CSRF token (base64url 编码)
func NewCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cookieutil: generate csrf token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SetCSRF 下发 CSRF Cookie (默认名 csrf_token);
// 该 Cookie 特意不设 HttpOnly, 前端需读取它并在写操作请求时
// 放入 X-CSRF-Token 请求头
func SetCSRF(w http.ResponseWriter, token string, opts Options) {
	o := opts.normalize()
	c := &http.Cookie{
		Name:     DefaultCSRFCookie,
		Value:    token,
		Domain:   o.Domain,
		Path:     o.Path,
		Secure:   o.Secure,
		SameSite: o.SameSite,
		HttpOnly: false, // double-submit 方案要求前端可读
	}
	if o.MaxAge > 0 {
		c.MaxAge = int(o.MaxAge / time.Second)
		c.Expires = time.Now().Add(o.MaxAge)
	}
	http.SetCookie(w, c)
}

// ValidateCSRF 按默认 Cookie 名与请求头名做 double-submit 校验
func ValidateCSRF(r *http.Request) bool {
	return ValidateCSRFNames(r, DefaultCSRFCookie, DefaultCSRFHeader)
}

// ValidateCSRFNames 校验请求的 CSRF token: Cookie 与请求头都存在、
// 非空且恒定时间比较一致时通过; 任一缺失即失败
func ValidateCSRFNames(r *http.Request, cookieName, headerName string) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	h := r.Header.Get(headerName)
	if h == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(h)) == 1
}
