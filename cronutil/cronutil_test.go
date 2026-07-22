package cronutil

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdleBackoff(t *testing.T) {
	b := NewIdleBackoff(50*time.Millisecond, 200*time.Millisecond)

	if !b.ShouldRun() {
		t.Fatal("初始状态应允许执行")
	}

	// 第一次空轮: 退避 50ms
	b.Miss()
	if b.ShouldRun() {
		t.Error("空轮后应处于退避窗口")
	}
	time.Sleep(60 * time.Millisecond)
	if !b.ShouldRun() {
		t.Error("退避窗口过后应允许执行")
	}

	// 连续空轮指数增长并封顶: 100ms → 200ms → 200ms
	b.Miss()
	b.Miss()
	b.Miss()
	if b.cur != 200*time.Millisecond {
		t.Errorf("退避间隔应封顶 200ms, got %v", b.cur)
	}

	// 有待办后立即清零
	b.Hit()
	if !b.ShouldRun() {
		t.Error("Hit 后应立即允许执行")
	}

	// 手动触发绕过退避
	b.Miss()
	b.Reset()
	if !b.ShouldRun() {
		t.Error("Reset 后应立即允许执行")
	}
}

func TestTickGuard(t *testing.T) {
	var g TickGuard

	started := make(chan struct{})
	release := make(chan struct{})
	go g.TryRun(func() {
		close(started)
		<-release
	})
	<-started

	// 上一轮未结束时, 下一轮直接跳过
	if g.TryRun(func() {}) {
		t.Error("重叠执行应被跳过")
	}

	close(release)
	// 等上一轮释放后可再次执行
	deadline := time.After(time.Second)
	for {
		if g.TryRun(func() {}) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("释放后应可再次执行")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestTickGuardPanicReleases(t *testing.T) {
	var g TickGuard

	func() {
		defer func() { recover() }()
		g.TryRun(func() { panic("boom") })
	}()

	if !g.TryRun(func() {}) {
		t.Error("panic 后闸门应已释放")
	}
}

func TestDebouncedThreshold(t *testing.T) {
	var mu sync.Mutex
	var batches [][]int

	d := NewDebounced(3, time.Hour, func(items []int) {
		mu.Lock()
		batches = append(batches, items)
		mu.Unlock()
	})

	d.Add(1, 2)
	mu.Lock()
	if len(batches) != 0 {
		t.Error("未达阈值不应刷新")
	}
	mu.Unlock()

	d.Add(3) // 达到阈值立即刷新
	mu.Lock()
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Errorf("阈值刷新异常: %v", batches)
	}
	mu.Unlock()
}

func TestDebouncedWindow(t *testing.T) {
	flushed := make(chan []int, 1)
	d := NewDebounced(100, 50*time.Millisecond, func(items []int) {
		flushed <- items
	})

	d.Add(1, 2)
	select {
	case batch := <-flushed:
		if len(batch) != 2 {
			t.Errorf("时间窗刷新批量 = %d, expected 2", len(batch))
		}
	case <-time.After(time.Second):
		t.Fatal("时间窗到期未触发刷新")
	}
}

func TestDebouncedClose(t *testing.T) {
	var count atomic.Int64
	d := NewDebounced(100, time.Hour, func(items []int) {
		count.Add(int64(len(items)))
	})

	d.Add(1, 2, 3)
	d.Close()
	if count.Load() != 3 {
		t.Errorf("Close 应刷新剩余条目, flushed = %d", count.Load())
	}

	// Close 后 Add 退化为同步直发, 不丢数据
	d.Add(4, 5)
	if count.Load() != 5 {
		t.Errorf("Close 后 Add 应直发, flushed = %d", count.Load())
	}
}

func TestDebouncedConcurrentAdd(t *testing.T) {
	var count atomic.Int64
	d := NewDebounced(10, 10*time.Millisecond, func(items []int) {
		count.Add(int64(len(items)))
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				d.Add(j)
			}
		}()
	}
	wg.Wait()
	d.Close()

	if count.Load() != 800 {
		t.Errorf("并发 Add 后总条数 = %d, expected 800 (不丢不重)", count.Load())
	}
}
