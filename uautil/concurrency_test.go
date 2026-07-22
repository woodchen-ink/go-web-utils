package uautil

import (
	"sync"
	"testing"
)

// TestConcurrentPatternAccess 验证特征列表的增删与检测函数可安全并发调用
// (配合 go test -race 检测数据竞争)
func TestConcurrentPatternAccess(t *testing.T) {
	var wg sync.WaitGroup

	// 并发读: 机器人与浏览器检测
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				IsBotUserAgent("Mozilla/5.0 (Windows NT 10.0) Chrome/120.0", true)
				IsBotUserAgent("python-requests/2.31", false)
				IsBrowserUserAgent("Mozilla/5.0 (Windows NT 10.0) Chrome/120.0")
				GetBotPatterns()
				GetLegitimatePatterns()
				GetBrowserPatterns()
			}
		}()
	}

	// 并发写: 添加并移除自定义特征
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				removeBot := AddCustomBotPattern("race-test-bot")
				removeLegit := AddLegitimateBot("race-test-legit")
				removeBrowser := AddCustomBrowserPattern("race-test-browser/")
				removeBot()
				removeLegit()
				removeBrowser()
			}
		}()
	}

	wg.Wait()

	// 收尾校验: 所有临时特征都已被移除
	for _, p := range GetBotPatterns() {
		if p == "race-test-bot" {
			t.Error("race-test-bot 未被完全移除")
		}
	}
	for _, p := range GetLegitimatePatterns() {
		if p == "race-test-legit" {
			t.Error("race-test-legit 未被完全移除")
		}
	}
	for _, p := range GetBrowserPatterns() {
		if p == "race-test-browser/" {
			t.Error("race-test-browser/ 未被完全移除")
		}
	}
}
