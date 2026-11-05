package app

import (
	"sync"
	"testing"
	"time"
)

// TestWaitTimeoutReturnsWhenTasksFinish 确认后台任务退出后立刻继续关闭，
// 而不是总睡满超时。
func TestWaitTimeoutReturnsWhenTasksFinish(t *testing.T) {
	var wg sync.WaitGroup
	wg.Go(func() { time.Sleep(10 * time.Millisecond) })

	start := time.Now()
	if !waitTimeout(&wg, time.Second) {
		t.Fatal("任务已结束时应返回 true")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("不应等满超时")
	}
}

// TestWaitTimeoutGivesUpOnStuckTask 防的是一个卡死的后台任务让进程永远退不出。
func TestWaitTimeoutGivesUpOnStuckTask(t *testing.T) {
	var wg sync.WaitGroup
	block := make(chan struct{})
	defer close(block)
	wg.Go(func() { <-block })

	if waitTimeout(&wg, 20*time.Millisecond) {
		t.Fatal("任务未结束时应超时返回 false")
	}
}
