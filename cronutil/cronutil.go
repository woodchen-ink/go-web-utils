/*
Package cronutil 提供定时任务与后台作业的三个性能防护组件。

高频 tick 任务、批处理循环、批量写外部系统, 是单核 CPU 被打满的高发点。
本包对应三条防护规则:

  - IdleBackoff: 空轮指数退避闸门。"每秒 tick → 扫描待办"型任务在待办清空后
    仍每 tick 全量扫描会空转打满 CPU; 退避判断应放在查库之前。
  - TickGuard: 单实例防重叠。单轮 tick 耗时可能超过触发间隔, 上一轮未结束时
    下一轮直接跳过, 避免并发重叠扫描同一批待办。
  - Debounced: 去抖聚合队列。高频批量写外部系统 (索引/搜索引擎/webhook) 时,
    攒到阈值条数或时间窗口才刷一次, 把"每批一提交"合并为"跨批攒一提交"。

使用示例 (cron tick):

	backoff := cronutil.NewIdleBackoff(2*time.Second, time.Minute)
	var guard cronutil.TickGuard

	// 每秒 tick
	if !backoff.ShouldRun() {
		return // 仍在退避窗口, 不查库
	}
	guard.TryRun(func() {
		items := loadPending()
		if len(items) == 0 {
			backoff.Miss()
			return
		}
		backoff.Hit()
		process(items)
	})

内存队列重启会丢数据, 使用 Debounced 时需配合定期全量/增量同步兜底。

完整文档: https://go-web-utils.czl.net/docs/cronutil
*/
package cronutil

import (
	"sync"
	"sync/atomic"
	"time"
)

// IdleBackoff 空轮指数退避闸门。连续空轮时按指数延长下次允许执行的间隔,
// 有新待办立即清零恢复高频。状态放进程内存即可, 不需要持久化跨重启。
type IdleBackoff struct {
	mu          sync.Mutex
	base        time.Duration
	max         time.Duration
	cur         time.Duration // 当前退避间隔; 0 表示无退避
	nextAllowed time.Time
}

// NewIdleBackoff 创建退避闸门; base 为首次空轮后的退避间隔, max 为退避上限
// (如 2s 起步、翻倍增长、封顶 60s)
func NewIdleBackoff(base, max time.Duration) *IdleBackoff {
	if base <= 0 {
		base = time.Second
	}
	if max < base {
		max = base
	}
	return &IdleBackoff{base: base, max: max}
}

// ShouldRun 判断本轮 tick 是否允许执行; 仍在退避窗口内时返回 false,
// 调用方应直接返回, 不查库也不写任务状态
func (b *IdleBackoff) ShouldRun() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !time.Now().Before(b.nextAllowed)
}

// Hit 报告本轮有待办被处理, 清零退避、恢复高频扫描
func (b *IdleBackoff) Hit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cur = 0
	b.nextAllowed = time.Time{}
}

// Miss 报告本轮空转, 退避间隔翻倍 (首次为 base, 封顶 max),
// 并推迟下次允许执行的时间
func (b *IdleBackoff) Miss() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur == 0 {
		b.cur = b.base
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	b.nextAllowed = time.Now().Add(b.cur)
}

// Reset 清空退避状态, 用于管理员手动触发时绕过退避窗口强制执行
func (b *IdleBackoff) Reset() {
	b.Hit()
}

// TickGuard 单实例防重叠闸门; 零值即可用。
// 上一轮任务未返回时, 下一轮 TryRun 直接跳过, 不排队等待
type TickGuard struct {
	running atomic.Bool
}

// TryRun 尝试执行 fn: 当前无其他执行中的轮次时运行并返回 true,
// 否则不执行直接返回 false。fn panic 时闸门也会正确释放
func (g *TickGuard) TryRun(fn func()) bool {
	if !g.running.CompareAndSwap(false, true) {
		return false
	}
	defer g.running.Store(false)
	fn()
	return true
}

// Debounced 去抖聚合队列: 攒到阈值条数或时间窗口先到者触发一次批量刷新。
// 即时直发仅限低频单条场景; 采集/回灌/批量管道应走本队列。
//
// 刷新回调串行执行且批次间保持 FIFO 顺序; 回调内可安全调用本实例的
// Add/Flush/Close (如失败回灌重试), 不会死锁 —— 重入产生的批次会在当前
// 批回调返回后继续被同一轮 drain 处理
type Debounced[T any] struct {
	mu        sync.Mutex
	buf       []T   // 未成批的缓冲
	pending   [][]T // 已成批待刷新的 FIFO 队列
	threshold int
	window    time.Duration
	flushFn   func([]T)
	timer     *time.Timer
	closed    bool

	draining atomic.Bool // 同一时刻只有一个 goroutine 执行 drain, 保证回调串行且 FIFO
}

// NewDebounced 创建去抖队列; threshold 为触发刷新的条数阈值,
// window 为最长等待时间窗, flush 为批量刷新回调 (串行调用, 不会并发)
func NewDebounced[T any](threshold int, window time.Duration, flush func([]T)) *Debounced[T] {
	if threshold <= 0 {
		threshold = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &Debounced[T]{threshold: threshold, window: window, flushFn: flush}
}

// Add 追加待刷新条目; 达到阈值立即成批, 否则等待时间窗。
// Close 之后调用同样入队并立即触发刷新, 不丢数据
func (d *Debounced[T]) Add(items ...T) {
	if len(items) == 0 {
		return
	}

	d.mu.Lock()
	d.buf = append(d.buf, items...)
	if d.closed || len(d.buf) >= d.threshold {
		d.enqueueLocked()
	} else if d.timer == nil {
		// 首条入队时启动时间窗定时器
		d.timer = time.AfterFunc(d.window, d.Flush)
	}
	d.mu.Unlock()

	d.drain()
}

// Flush 立即把当前缓冲成批并触发刷新 (无论是否达到阈值); 缓冲为空时是空操作
func (d *Debounced[T]) Flush() {
	d.mu.Lock()
	d.enqueueLocked()
	d.mu.Unlock()
	d.drain()
}

// Close 关闭队列并刷新剩余条目; 之后的 Add 入队即刷 (退化为直发)。
// 在刷新回调内调用 Close 也安全: 剩余批次由外层 drain 循环继续处理
func (d *Debounced[T]) Close() {
	d.mu.Lock()
	d.closed = true
	d.enqueueLocked()
	d.mu.Unlock()
	d.drain()
}

// enqueueLocked 把缓冲区成批推入待刷队列并停掉时间窗定时器;
// 调用方必须持有 d.mu
func (d *Debounced[T]) enqueueLocked() {
	if len(d.buf) > 0 {
		d.pending = append(d.pending, d.buf)
		d.buf = nil
	}
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// drain 抢占唯一刷新权后按 FIFO 逐批执行回调; 抢占失败直接返回,
// 在刷的 goroutine 会通过收尾复查接手新入队的批次, 不会有批次滞留。
// 回调 panic 会被捕获并丢弃该批 —— 时间窗触发的刷新运行在定时器协程,
// 未捕获的 panic 会击穿整个进程; 业务错误与重试应在回调内自行处理
func (d *Debounced[T]) drain() {
	if d.flushFn == nil {
		return
	}
	for {
		if !d.draining.CompareAndSwap(false, true) {
			return // 已有 goroutine 在刷, 由它接手
		}
		for {
			d.mu.Lock()
			if len(d.pending) == 0 {
				d.mu.Unlock()
				break
			}
			batch := d.pending[0]
			d.pending = d.pending[1:]
			d.mu.Unlock()
			d.safeFlush(batch)
		}
		d.draining.Store(false)

		// 收尾复查: 释放刷新权与新批入队存在竞窗, 有新批则重新抢占
		d.mu.Lock()
		empty := len(d.pending) == 0
		d.mu.Unlock()
		if empty {
			return
		}
	}
}

// safeFlush 执行单批回调并捕获 panic
func (d *Debounced[T]) safeFlush(batch []T) {
	defer func() { _ = recover() }()
	d.flushFn(batch)
}
