package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// 抖动必须落在 [d/2, d)：既要散开重连时刻，又不能把退避缩短到低于承诺。
func TestWithJitterWithinRange(t *testing.T) {
	const d = 100 * time.Millisecond
	for i := 0; i < 500; i++ {
		got := withJitter(d)
		if got < d/2 || got >= d {
			t.Fatalf("withJitter(%v) = %v，应落在 [%v, %v)", d, got, d/2, d)
		}
	}
}

// 如果抖动写成了固定值（比如直接返回 d），所有 worker 仍会同时重连，
// 这个断言就是用来抓那种退化的。
func TestWithJitterSpreads(t *testing.T) {
	const d = time.Second
	seen := make(map[time.Duration]struct{}, 50)
	for i := 0; i < 50; i++ {
		seen[withJitter(d)] = struct{}{}
	}
	if len(seen) < 10 {
		t.Fatalf("50 次抖动只出现 %d 种取值，抖动没有生效", len(seen))
	}
}

// 出错后必须重试，且 ctx 取消时能及时退出。
func TestRunForeverRetriesUntilContextDone(t *testing.T) {
	consumer := &Consumer{}
	opt := supervisorOptions{
		initialBackoff: time.Millisecond,
		maxBackoff:     4 * time.Millisecond,
		stableAfter:    time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	calls := 0
	done := make(chan struct{})

	go func() {
		defer close(done)
		consumer.runForever(ctx, "test-worker", opt, func(context.Context) error {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n >= 5 {
				cancel()
			}
			return errors.New("模拟故障")
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 runForever 没有退出")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls < 5 {
		t.Fatalf("只执行了 %d 次，期望连续重试至少 5 次", calls)
	}
}

// 没有错误说明消费者自己正常结束了，不应该再拉起。
func TestRunForeverStopsWhenRunReturnsNil(t *testing.T) {
	consumer := &Consumer{}
	opt := supervisorOptions{
		initialBackoff: time.Millisecond,
		maxBackoff:     time.Millisecond,
		stableAfter:    time.Hour,
	}
	calls := 0
	consumer.runForever(context.Background(), "test-worker", opt, func(context.Context) error {
		calls++
		return nil
	})
	if calls != 1 {
		t.Fatalf("run 返回 nil 时应只执行 1 次，实际 %d 次", calls)
	}
}

// 退避要真的增长。用总耗时做判据：5 次执行若退避不增长会快得多。
func TestRunForeverBacksOffExponentially(t *testing.T) {
	consumer := &Consumer{}
	opt := supervisorOptions{
		initialBackoff: 4 * time.Millisecond,
		maxBackoff:     64 * time.Millisecond,
		stableAfter:    time.Hour, // 永不重置，保证退避一路增长
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	calls := 0
	started := time.Now()

	consumer.runForever(ctx, "test-worker", opt, func(context.Context) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n >= 5 {
			cancel()
			return nil
		}
		return errors.New("模拟故障")
	})

	elapsed := time.Since(started)
	// 期望等待序列 4→8→16→32ms（含抖动后落在 [2,4)+[4,8)+[8,16)+[16,32)），
	// 下限约 30ms；若退避没有增长，总耗时会在 10ms 以内。
	if elapsed < 25*time.Millisecond {
		t.Fatalf("5 次重试只用了 %v，退避看起来没有增长", elapsed)
	}
}

// 长时间稳定运行后再失败，应当把退避重置回初始值，而不是继续等满上限。
func TestRunForeverResetsBackoffAfterStableRun(t *testing.T) {
	consumer := &Consumer{}
	opt := supervisorOptions{
		initialBackoff: 100 * time.Millisecond,
		maxBackoff:     4 * time.Second,
		stableAfter:    0, // 每次都算"稳定过"，因此每次都重置
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	calls := 0
	started := time.Now()

	consumer.runForever(ctx, "test-worker", opt, func(context.Context) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n >= 5 {
			cancel()
			return nil
		}
		return errors.New("模拟故障")
	})

	elapsed := time.Since(started)
	// 每次都重置时，等待一直是 [50,100)ms，4 次合计 200-400ms；
	// 若没有重置，序列是 100→200→400→800ms，合计约 750-1500ms。
	if elapsed > 600*time.Millisecond {
		t.Fatalf("5 次重试用了 %v，退避没有被重置（重置应在 200-400ms）", elapsed)
	}
}
