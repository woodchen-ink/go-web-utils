/*
Package timex 提供项目级时区管理与时间边界计算。

Go 程序在容器内默认时区为 UTC, 依赖运行环境的 time.Local 会导致
"在我机器上是对的"问题。本包要求在程序入口显式初始化业务时区,
之后全项目通过 Loc() / Now() 使用统一时区, 不再触碰 time.Local。

使用示例 (main.go 入口):

	func main() {
		timex.MustInit("Asia/Shanghai") // 加载失败直接 panic, 不静默回退 UTC
		// ...
	}

	// 业务代码
	today := timex.StartOfDay(timex.Now())

存储层约定: 数据库时间字段一律以 UTC 存储, 仅在接口出口用本包换算展示时区。

完整文档: https://go-web-utils.czl.net/docs/timex
*/
package timex

import (
	"fmt"
	"sync/atomic"
	"time"
)

// appLoc 全局业务时区; 未初始化时为 nil, Loc() 会 panic 提示先初始化
var appLoc atomic.Pointer[time.Location]

// Init 加载并设置全局业务时区, 应在程序入口调用一次;
// 加载失败返回错误, 调用方应视为致命错误退出, 不要静默回退 UTC
func Init(name string) error {
	l, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("timex: load location %q: %w", name, err)
	}
	appLoc.Store(l)
	return nil
}

// MustInit 同 Init, 失败时 panic; 用于程序入口的快捷初始化
func MustInit(name string) {
	if err := Init(name); err != nil {
		panic(err)
	}
}

// Loc 返回全局业务时区; 未调用 Init/MustInit 时 panic,
// 强制暴露初始化遗漏而不是静默用错时区
func Loc() *time.Location {
	l := appLoc.Load()
	if l == nil {
		panic("timex: not initialized, call timex.Init or timex.MustInit at startup")
	}
	return l
}

// Now 返回业务时区下的当前时间
func Now() time.Time {
	return time.Now().In(Loc())
}

// In 把任意时间换算到业务时区
func In(t time.Time) time.Time {
	return t.In(Loc())
}

// StartOfDay 返回 t 所在自然日的起点 (业务时区 00:00:00)
func StartOfDay(t time.Time) time.Time {
	loc := Loc() // 只读一次, 避免运行期切换时区时换算与构造使用不同 Location
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// StartOfWeek 返回 t 所在自然周的起点 (业务时区周一 00:00:00)
func StartOfWeek(t time.Time) time.Time {
	day := StartOfDay(t)
	weekday := int(day.Weekday())
	if weekday == 0 {
		weekday = 7 // 周日折算为 7, 保证周一为一周起点
	}
	return day.AddDate(0, 0, -(weekday - 1))
}

// StartOfMonth 返回 t 所在自然月的起点 (业务时区 1 号 00:00:00)
func StartOfMonth(t time.Time) time.Time {
	loc := Loc()
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
}

// FormatRFC3339 按业务时区输出 RFC3339 字符串, 用于对外接口的时间字段
func FormatRFC3339(t time.Time) string {
	return t.In(Loc()).Format(time.RFC3339)
}

// ParseRFC3339 解析 RFC3339 时间字符串 (必须带时区偏移),
// 用于接收接口入参; 不接受省略时区的裸时间字符串
func ParseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
