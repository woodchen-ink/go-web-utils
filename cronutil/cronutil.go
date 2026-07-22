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
// 即时直发仅限低频单条场景; 采集/回灌/批量管道应走本队列
type Debounced[T any] struct {
	mu        sync.Mutex
	buf       []T
	threshold int
	window    time.Duration
	flushFn   func([]T)
	timer     *time.Timer
	closed    bool

	flushMu sync.Mutex // 串行化 flush 回调, 保证不并发执行
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

// Add 追加待刷新条目; 达到阈值立即触发刷新, 否则等待时间窗。
// Close 之后调用会立即同步刷新该批条目 (退化为直发), 不丢数据
func (d *Debounced[T]) Add(items ...T) {
	if len(items) == 0 {
		return
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.doFlush(items)
		return
	}
	d.buf = append(d.buf, items...)
	if len(d.buf) >= d.threshold {
		batch := d.takeLocked()
		d.mu.Unlock()
		d.doFlush(batch)
		return
	}
	// 首条入队时启动时间窗定时器
	if d.timer == nil {
		d.timer = time.AfterFunc(d.window, d.Flush)
	}
	d.mu.Unlock()
}

// Flush 立即刷新当前缓冲区 (无论是否达到阈值); 缓冲为空时是空操作
func (d *Debounced[T]) Flush() {
	d.mu.Lock()
	batch := d.takeLocked()
	d.mu.Unlock()
	d.doFlush(batch)
}

// Close 关闭队列并刷新剩余条目; 之后的 Add 退化为同步直发
func (d *Debounced[T]) Close() {
	d.mu.Lock()
	d.closed = true
	batch := d.takeLocked()
	d.mu.Unlock()
	d.doFlush(batch)
}

// takeLocked 取走缓冲区并停掉时间窗定时器; 调用方必须持有 d.mu
func (d *Debounced[T]) takeLocked() []T {
	batch := d.buf
	d.buf = nil
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	return batch
}

// doFlush 串行执行刷新回调
func (d *Debounced[T]) doFlush(batch []T) {
	if len(batch) == 0 || d.flushFn == nil {
		return
	}
	d.flushMu.Lock()
	defer d.flushMu.Unlock()
	d.flushFn(batch)
}
