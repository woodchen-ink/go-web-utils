package syncsdk

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// SubscribeOptions 配置 Subscribe。
type SubscribeOptions struct {
	// PollInterval 是事件流断开期间的回退轮询间隔, 默认 30 秒。
	PollInterval time.Duration
	// SafetyInterval 是事件流正常时的兜底全量同步间隔 (防止漏事件), 默认 10 分钟。
	SafetyInterval time.Duration
	// ReconnectMin / ReconnectMax 是事件流重连退避范围, 默认 1 秒 ~ 1 分钟。
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	// OnSync 在每轮同步结束时调用 (err 为 nil 表示成功), 可用来刷新界面或展示错误。
	OnSync func(res SyncResult, err error)
}

func (o SubscribeOptions) withDefaults() SubscribeOptions {
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	if o.SafetyInterval <= 0 {
		o.SafetyInterval = 10 * time.Minute
	}
	if o.ReconnectMin <= 0 {
		o.ReconnectMin = time.Second
	}
	if o.ReconnectMax <= 0 {
		o.ReconnectMax = time.Minute
	}
	return o
}

// Subscribe 持续同步一个空间, 直到 ctx 结束 (返回 ctx.Err())。
//
//   - 启动时同步一轮; 之后 SSE 收到 changed (序号大于本地游标) 或本地 Save / Delete 时再同步, 多次触发合并为一轮
//   - SSE 断开时按指数退避重连, 断开期间每 PollInterval 轮询同步一次; SSE 正常时每 SafetyInterval 兜底一次
//   - 同步失败经 OnSync 报告, 不中断订阅; 下次触发或轮询时重试
func (e *Engine) Subscribe(ctx context.Context, space string, opts SubscribeOptions) error {
	opts = opts.withDefaults()
	trigger := make(chan struct{}, 1)
	fire := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	var sseUp atomic.Bool
	var lastSeq atomic.Int64
	if c, err := e.opts.Cursors.Get(ctx, space); err == nil {
		lastSeq.Store(c)
	}

	go e.runEvents(ctx, space, opts, &sseUp, &lastSeq, fire)
	go func() {
		local := e.notifyChan(space)
		poll := time.NewTicker(opts.PollInterval)
		defer poll.Stop()
		lastSync := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-local:
				fire()
			case <-poll.C:
				if !sseUp.Load() || time.Since(lastSync) >= opts.SafetyInterval {
					lastSync = time.Now()
					fire()
				}
			}
		}
	}()

	fire()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-trigger:
			res, err := e.Sync(ctx, space)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil {
				lastSeq.Store(res.Pull.Cursor)
			}
			if opts.OnSync != nil {
				opts.OnSync(res, err)
			}
		}
	}
}

// runEvents 维持 SSE 连接: changed 事件序号超过已知游标时触发同步, presence 事件转给 OnPresence。
func (e *Engine) runEvents(ctx context.Context, space string, opts SubscribeOptions, up *atomic.Bool, lastSeq *atomic.Int64, fire func()) {
	delay := opts.ReconnectMin
	for ctx.Err() == nil {
		stream, err := e.opts.Client.Events(ctx, space, lastSeq.Load())
		if err == nil {
			up.Store(true)
			delay = opts.ReconnectMin
			fire() // 连接期间可能错过事件, 连上后补一轮
			for {
				ev, err := stream.Next()
				if err != nil {
					break
				}
				switch ev.Type {
				case EventChanged:
					if ev.Seq > lastSeq.Load() {
						fire()
					}
				case EventPresence:
					if e.opts.OnPresence != nil {
						e.opts.OnPresence(space, ev.Presence)
					}
				}
			}
			stream.Close()
			up.Store(false)
		} else if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
			// 鉴权问题由同步轮次经 OnSync 报告; 这里放慢重连。
			delay = opts.ReconnectMax
		}
		if ctx.Err() != nil {
			return
		}
		fire() // 断线后立即轮询一次, 之后由轮询器接管
		if sleep(ctx, jitter(delay)) != nil {
			return
		}
		delay = min(delay*2, opts.ReconnectMax)
	}
}

// jitter 返回 [d/2, d) 的随机时长, 避免大量客户端同时重连。
func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	return d/2 + RetryPolicy{BaseDelay: d / 2, MaxDelay: d / 2}.backoff(1)
}
