package syncsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TokenSource 提供 CZL Connect 用户访问令牌 (OAuth2 bearer)。实现方负责缓存与刷新。
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// TokenFunc 把函数适配为 TokenSource。
type TokenFunc func(ctx context.Context) (string, error)

// Token 实现 TokenSource。
func (f TokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

// TokenInvalidator 是 TokenSource 的可选扩展: 收到 401 时 SDK 调用 InvalidateToken
// 并用新令牌重试一次, 实现方据此强制刷新 (如用 refresh token 换新)。
type TokenInvalidator interface {
	InvalidateToken(token string)
}

// RetryPolicy 是瞬时错误 (网络故障、429、5xx) 的重试策略:
// 指数退避 + 全抖动 (full jitter), 服务端给了 Retry-After 时以它为准。
type RetryPolicy struct {
	// MaxAttempts 是含首次在内的最多尝试次数, 默认 4; 1 表示不重试。
	MaxAttempts int
	// BaseDelay 是首次退避上限, 默认 500ms, 之后每次翻倍。
	BaseDelay time.Duration
	// MaxDelay 是单次退避上限, 默认 30s。
	MaxDelay time.Duration
	// MaxRetryAfter 是愿意等待的 Retry-After 上限, 默认 2 分钟; 超过则直接返回错误。
	MaxRetryAfter time.Duration
}

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 4
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 500 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 30 * time.Second
	}
	if p.MaxRetryAfter <= 0 {
		p.MaxRetryAfter = 2 * time.Minute
	}
	return p
}

// backoff 返回第 attempt 次 (从 1 起) 失败后的等待时长: [0, min(MaxDelay, Base·2^(n-1))) 均匀随机。
func (p RetryPolicy) backoff(attempt int) time.Duration {
	ceil := p.BaseDelay << min(attempt-1, 20)
	if ceil <= 0 || ceil > p.MaxDelay {
		ceil = p.MaxDelay
	}
	return time.Duration(rand.Int64N(int64(ceil)) + 1)
}

// ClientOptions 配置 Client。
type ClientOptions struct {
	// BaseURL 是同步接口根地址, 含 /api/sync/v1, 例如 "https://connect.czl.net/api/sync/v1"。
	BaseURL string
	// Tokens 提供用户访问令牌, 必填。
	Tokens TokenSource
	// HTTPClient 默认 http.DefaultClient 的副本 (不设整体超时, SSE 长连接需要)。
	// 跨域 302 (附件预签名地址) 时 net/http 会自动去掉 Authorization 头。
	HTTPClient *http.Client
	// Retry 是瞬时错误的重试策略。
	Retry RetryPolicy
	// UserAgent 默认 "go-web-utils-syncsdk"。
	UserAgent string
	// DeviceID 是本机稳定的设备标识 (1~64 位字母、数字、'-'、'_'、'.'), 每台设备生成一次并持久化
	// (见 LoadOrCreateDeviceID), 不要随配置同步到其他设备。设置后每个请求带 X-Sync-Device 头,
	// 「正在编辑」按设备区分: 同一账号另一台设备上的心跳也会下发。留空时按账号区分 (看不到自己的任何设备)。
	DeviceID string
	// DeviceName 是设备显示名 (如 "办公室台式机"), 随 presence 下发给空间成员; 可留空, 超过 64 字符截断。
	DeviceName string
}

// Client 是 /api/sync/v1 的类型化 HTTP 客户端: 鉴权、错误归类、重试退避与上下文取消。
// 它不做加解密, 也不维护本地状态; 同步流程见 Engine。并发安全。
type Client struct {
	base   *url.URL
	tokens TokenSource
	hc     *http.Client
	retry  RetryPolicy
	ua     string
	device string
	name   string // 已百分号编码
}

// NewClient 创建 Client。
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.Tokens == nil {
		return nil, errors.New("syncsdk: ClientOptions.Tokens is required")
	}
	u, err := url.Parse(strings.TrimRight(opts.BaseURL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("syncsdk: invalid BaseURL %q", opts.BaseURL)
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	if opts.DeviceID != "" && !ValidDeviceID(opts.DeviceID) {
		return nil, fmt.Errorf("syncsdk: invalid DeviceID %q", opts.DeviceID)
	}
	var name string
	if opts.DeviceID != "" {
		name = url.PathEscape(CleanDeviceName(opts.DeviceName))
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "go-web-utils-syncsdk"
	}
	return &Client{base: u, tokens: opts.Tokens, hc: hc, retry: opts.Retry.withDefaults(), ua: ua,
		device: opts.DeviceID, name: name}, nil
}

// request 描述一次调用。body 为可重放的字节, 或由 open 在每次尝试时重新打开 (大附件)。
type request struct {
	method  string
	path    []string // 逐段转义
	query   url.Values
	body    []byte
	json    bool
	open    func() (io.ReadCloser, error)
	size    int64
	ctype   string
	headers http.Header
	noRetry bool
}

func (c *Client) url(r *request) string {
	u := *c.base
	var sb strings.Builder
	sb.WriteString(u.EscapedPath())
	for _, seg := range r.path {
		sb.WriteByte('/')
		sb.WriteString(url.PathEscape(seg))
	}
	u.RawPath = sb.String()
	u.Path, _ = url.PathUnescape(u.RawPath)
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}
	return u.String()
}

// send 执行请求并在瞬时错误时重试; 返回的响应状态为 2xx 或 3xx (已跟随跳转),
// 调用方负责关闭 Body。非 2xx 统一转成 *APIError。
func (c *Client) send(ctx context.Context, r *request) (*http.Response, error) {
	refreshed := false
	for attempt := 1; ; attempt++ {
		resp, err := c.once(ctx, r)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized && !refreshed {
			if inv, ok := c.tokens.(TokenInvalidator); ok {
				if tok, terr := c.tokens.Token(ctx); terr == nil {
					inv.InvalidateToken(tok)
				}
				refreshed = true
				attempt--
				continue
			}
		}
		if r.noRetry || !IsTransient(err) || attempt >= c.retry.MaxAttempts {
			return nil, err
		}
		wait := c.retry.backoff(attempt)
		if ae != nil && ae.RetryAfter > 0 {
			if ae.RetryAfter > c.retry.MaxRetryAfter {
				return nil, err
			}
			wait = ae.RetryAfter
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (c *Client) once(ctx context.Context, r *request) (*http.Response, error) {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("syncsdk: token: %w", err)
	}
	var body io.Reader
	switch {
	case r.open != nil:
		rc, err := r.open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		body = rc
	case r.body != nil:
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.url(r), body)
	if err != nil {
		return nil, err
	}
	if r.open != nil && r.size >= 0 {
		req.ContentLength = r.size
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	if c.device != "" {
		req.Header.Set(HeaderDevice, c.device)
		if c.name != "" {
			req.Header.Set(HeaderDeviceName, c.name)
		}
	}
	if r.json {
		req.Header.Set("Content-Type", "application/json")
	} else if r.ctype != "" {
		req.Header.Set("Content-Type", r.ctype)
	}
	for k, vs := range r.headers {
		req.Header[k] = vs
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return resp, nil
	}
	defer resp.Body.Close()
	return nil, decodeError(resp)
}

// envelope 是 resputil 的统一响应结构。
type envelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

const maxErrorBody = 4 << 20 // 409 附带的当前文档最大 1 MB + 余量

func decodeError(resp *http.Response) error {
	ae := &APIError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var env envelope
	if json.Unmarshal(raw, &env) == nil {
		ae.Code, ae.Msg, ae.Data = env.Code, env.Msg, env.Data
	}
	if ae.Code == 0 {
		ae.Code = resp.StatusCode * 100
	}
	return ae
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// doJSON 发送 JSON 请求并把成功响应的 data 解到 out (out 可为 nil)。
// 服务端以 HTTP 200 + 非 200 业务码返回错误时, 按业务码前三位还原 HTTP 状态。
func (c *Client) doJSON(ctx context.Context, r *request, in, out any) error {
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		r.body, r.json = b, true
	}
	resp, err := c.send(ctx, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%w: response is not JSON", ErrProtocol)
	}
	if env.Code != 0 && env.Code != 200 {
		status := env.Code
		for status >= 1000 {
			status /= 10
		}
		if status < 400 || status > 599 {
			status = http.StatusBadRequest
		}
		return &APIError{Status: status, Code: env.Code, Msg: env.Msg, Data: env.Data}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
