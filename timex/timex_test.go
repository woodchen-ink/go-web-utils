package timex

import (
	"testing"
	"time"
)

func TestLocPanicsBeforeInit(t *testing.T) {
	// 本测试必须最先运行前提不可靠, 改为用独立的未初始化状态验证:
	// 先清空全局时区再断言 panic, 结束后恢复
	prev := appLoc.Load()
	appLoc.Store(nil)
	defer func() {
		if prev != nil {
			appLoc.Store(prev)
		}
	}()

	defer func() {
		if recover() == nil {
			t.Error("未初始化调用 Loc() 应 panic")
		}
	}()
	Loc()
}

func TestInitInvalidName(t *testing.T) {
	if err := Init("Not/AZone"); err == nil {
		t.Error("非法时区名应返回错误")
	}
}

func TestBoundaries(t *testing.T) {
	MustInit("Asia/Shanghai")

	// 2026-07-22 是周三; 用 UTC 输入验证跨时区换算:
	// UTC 2026-07-21 17:00 = 上海 2026-07-22 01:00
	input := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)

	day := StartOfDay(input)
	if day.Format("2006-01-02 15:04:05") != "2026-07-22 00:00:00" {
		t.Errorf("StartOfDay = %v", day)
	}
	if day.Location().String() != "Asia/Shanghai" {
		t.Errorf("StartOfDay 时区 = %v", day.Location())
	}

	week := StartOfWeek(input)
	if week.Format("2006-01-02") != "2026-07-20" { // 周一
		t.Errorf("StartOfWeek = %v", week)
	}

	month := StartOfMonth(input)
	if month.Format("2006-01-02 15:04:05") != "2026-07-01 00:00:00" {
		t.Errorf("StartOfMonth = %v", month)
	}
}

func TestStartOfWeekOnSunday(t *testing.T) {
	MustInit("Asia/Shanghai")

	// 2026-07-26 是周日, 所在周的周一是 2026-07-20
	sunday := time.Date(2026, 7, 26, 12, 0, 0, 0, Loc())
	if got := StartOfWeek(sunday).Format("2006-01-02"); got != "2026-07-20" {
		t.Errorf("周日的 StartOfWeek = %v, expected 2026-07-20", got)
	}
}

func TestRFC3339RoundTrip(t *testing.T) {
	MustInit("Asia/Shanghai")

	src := time.Date(2026, 7, 22, 10, 30, 0, 0, time.UTC)
	s := FormatRFC3339(src)
	if s != "2026-07-22T18:30:00+08:00" {
		t.Errorf("FormatRFC3339 = %v", s)
	}

	parsed, err := ParseRFC3339(s)
	if err != nil {
		t.Fatalf("ParseRFC3339: %v", err)
	}
	if !parsed.Equal(src) {
		t.Errorf("往返不一致: %v != %v", parsed, src)
	}

	// 省略时区的裸字符串必须报错
	if _, err := ParseRFC3339("2026-07-22 18:30:00"); err == nil {
		t.Error("裸时间字符串应解析失败")
	}
}
