package syncsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// 按 HTTP 状态归类的哨兵错误, 用 errors.Is 判断; 具体业务码与英文短语见 *APIError。
var (
	ErrUnauthorized  = errors.New("syncsdk: unauthorized")      // 401 令牌无效或过期
	ErrForbidden     = errors.New("syncsdk: forbidden")         // 403 无权限、只读、集合不可写、只追加集合
	ErrNotFound      = errors.New("syncsdk: not found")         // 404
	ErrConflict      = errors.New("syncsdk: conflict")          // 409 版本冲突或状态冲突
	ErrCursorExpired = errors.New("syncsdk: cursor expired")    // 410 游标早于墓碑保留期, 需全量重拉
	ErrTooLarge      = errors.New("syncsdk: payload too large") // 413 单条超限或超配额
	ErrRateLimited   = errors.New("syncsdk: rate limited")      // 429
	ErrServer        = errors.New("syncsdk: server error")      // 5xx
)

// SDK 本地错误。
var (
	// ErrLocked 表示本机没有用户私钥 (未开通或尚未在本设备解锁)。
	ErrLocked = errors.New("syncsdk: user key not available on this device")
	// ErrNotEnrolled 表示服务端还没有该用户的公钥 (从未开通端到端加密)。
	ErrNotEnrolled = errors.New("syncsdk: end-to-end encryption not enrolled")
	// ErrKeyNotGranted 表示本人在该空间还没有任何密钥 (待 owner / admin 授予)。
	ErrKeyNotGranted = errors.New("syncsdk: space key not granted yet")
	// ErrAppendOnly 表示对只追加集合做了修改或删除。
	ErrAppendOnly = errors.New("syncsdk: collection is append-only")
	// ErrRequestExpired 表示新设备授权请求已过期。
	ErrRequestExpired = errors.New("syncsdk: device request expired")
	// ErrRequestRejected 表示新设备授权请求被拒绝或撤回。
	ErrRequestRejected = errors.New("syncsdk: device request rejected")
	// ErrProtocol 表示服务端返回的数据不符合协议 (承诺不符、字段缺失等), 应视为攻击或故障。
	ErrProtocol = errors.New("syncsdk: protocol violation")
)

// APIError 是服务端返回的错误。
type APIError struct {
	// Status 为 HTTP 状态; 服务端用 HTTP 200 + 业务码返回错误时按业务码前三位推出。
	Status int
	// Code 与 Msg 为业务码与 snake_case 英文短语。
	Code int
	Msg  string
	// RetryAfter 来自 Retry-After 头, 没有为 0。
	RetryAfter time.Duration
	// Data 是错误响应的 data 原文; 409 时为服务端当前文档或记录。
	Data json.RawMessage
}

func (e *APIError) Error() string {
	return fmt.Sprintf("syncsdk: http %d code %d %s", e.Status, e.Code, e.Msg)
}

// Is 让 errors.Is(err, ErrConflict) 等按状态归类匹配。
func (e *APIError) Is(target error) bool { return statusSentinel(e.Status) == target }

// Current 解出 409 附带的服务端当前文档; 没有时返回 nil。
func (e *APIError) Current() *Doc {
	if e.Status != http.StatusConflict || len(e.Data) == 0 || string(e.Data) == "null" {
		return nil
	}
	var d Doc
	if json.Unmarshal(e.Data, &d) != nil || d.ID == "" {
		return nil
	}
	return &d
}

func statusSentinel(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return ErrUnauthorized
	case status == http.StatusForbidden:
		return ErrForbidden
	case status == http.StatusNotFound:
		return ErrNotFound
	case status == http.StatusConflict:
		return ErrConflict
	case status == http.StatusGone:
		return ErrCursorExpired
	case status == http.StatusRequestEntityTooLarge:
		return ErrTooLarge
	case status == http.StatusTooManyRequests:
		return ErrRateLimited
	case status >= 500:
		return ErrServer
	}
	return nil
}

// IsTransient 判断错误是否值得稍后重试: 网络故障、429、5xx。
// 409 / 403 / 413 等由调用方按语义处理, 上下文取消与本地错误也不算瞬时错误。
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusTooManyRequests || ae.Status >= 500
	}
	var ne net.Error
	var ue *url.Error
	return errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, io.ErrUnexpectedEOF)
}
