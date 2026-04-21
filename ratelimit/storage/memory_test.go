package storage_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-ratelimit/ratelimit/storage"
)

const (
	capacity   = 5
	refillRate = 1.0

	fwLimit      = int64(5)
	fwWindowSize = 60 * time.Second

	swcLimit      = int64(5)
	swcWindowSize = 60 * time.Second
	// now_ms=1_000_000 のとき:
	//   window_sec=60, curr_window_start=960s (elapsed=40s), overlap_rate≈0.333
	//   prev window: [900_000ms, 960_000ms)
	//   curr window: [960_000ms, 1_020_000ms)
	swcNowMs = int64(1_000_000)
)

func baseArgs(nowMs int64) storage.TokenBucketArgs {
	return storage.TokenBucketArgs{
		Capacity:   capacity,
		RefillRate: refillRate,
		NowMs:      nowMs,
	}
}

func fwArgs(nowMs int64) storage.FixedWindowArgs {
	return storage.FixedWindowArgs{
		Limit:      fwLimit,
		WindowSize: fwWindowSize,
		NowMs:      nowMs,
	}
}

func swcArgs(nowMs int64) storage.SlidingWindowCounterArgs {
	return storage.SlidingWindowCounterArgs{
		Limit:      swcLimit,
		WindowSize: swcWindowSize,
		NowMs:      nowMs,
	}
}

// --- 基本動作 ---

func TestMemoryStorage_TokenBucket_AllowsUpToCapacity(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < capacity; i++ {
		res, err := s.Run(ctx, "user:alice:rl", baseArgs(now))
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	res, err := s.Run(ctx, "user:alice:rl", baseArgs(now))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("6th request should be denied")
	}
	if res.ResetMS <= 0 {
		t.Fatal("ResetMS should be positive when denied")
	}
}

func TestMemoryStorage_TokenBucket_DifferentKeysAreIndependent(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < capacity; i++ {
		s.Run(ctx, "user:alice:rl", baseArgs(now))
	}

	res, err := s.Run(ctx, "user:bob:rl", baseArgs(now))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("bob should not be affected by alice's bucket")
	}
}

func TestMemoryStorage_TokenBucket_RemainingDecreases(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	prev := capacity
	for i := 0; i < capacity; i++ {
		res, _ := s.Run(ctx, "user:carol:rl", baseArgs(now))
		if res.Remaining >= prev {
			t.Fatalf("remaining should decrease: got %d, prev %d", res.Remaining, prev)
		}
		prev = res.Remaining
	}
}

// --- 時刻注入によるリフィル検証 ---
// NowMs を注入できるおかげで time.Sleep なしでリフィルをテストできる

func TestMemoryStorage_TokenBucket_RefillOverTime(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	// バケツを使い切る
	for i := 0; i < capacity; i++ {
		s.Run(ctx, "user:dave:rl", baseArgs(now))
	}

	// capacity 分の時間(ms)が経過したとして NowMs を進める
	// refillRate=1 token/sec, capacity=5 なので 5000ms 後に満タン
	later := now + int64(capacity/refillRate*1000)
	res, err := s.Run(ctx, "user:dave:rl", baseArgs(later))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("should be allowed after tokens refilled")
	}
}

// --- 競合状態の検証 (go test -race) ---

func TestMemoryStorage_TokenBucket_Race(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	const goroutines = 100
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := s.Run(ctx, "user:race:rl", baseArgs(now))
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() > capacity {
		t.Fatalf("allowed %d requests, but capacity is %d — mutex broken", allowed.Load(), capacity)
	}
}

// ========================
// Fixed Window
// ========================

func TestMemoryStorage_FixedWindow_AllowsUpToLimit(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < int(fwLimit); i++ {
		res, err := s.Run(ctx, "user:alice:fw", fwArgs(now))
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	res, err := s.Run(ctx, "user:alice:fw", fwArgs(now))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("request over limit should be denied")
	}
	if res.ResetMS <= 0 {
		t.Fatal("ResetMS should be positive when denied")
	}
}

func TestMemoryStorage_FixedWindow_DifferentKeysAreIndependent(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < int(fwLimit); i++ {
		s.Run(ctx, "user:alice:fw", fwArgs(now))
	}

	res, err := s.Run(ctx, "user:bob:fw", fwArgs(now))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("bob should not be affected by alice's window")
	}
}

func TestMemoryStorage_FixedWindow_RemainingDecreases(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	prev := int(fwLimit)
	for i := 0; i < int(fwLimit); i++ {
		res, _ := s.Run(ctx, "user:carol:fw", fwArgs(now))
		if res.Remaining >= prev {
			t.Fatalf("remaining should decrease: got %d, prev %d", res.Remaining, prev)
		}
		prev = res.Remaining
	}
}

// --- 時刻注入によるウィンドウリセット検証 ---

func TestMemoryStorage_FixedWindow_ResetsAfterWindow(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < int(fwLimit); i++ {
		s.Run(ctx, "user:dave:fw", fwArgs(now))
	}

	res, _ := s.Run(ctx, "user:dave:fw", fwArgs(now))
	if res.Allowed {
		t.Fatal("should be denied before window expires")
	}

	// NowMs を次のウィンドウ先頭に進める
	nextWindow := now + fwWindowSize.Milliseconds()
	res, err := s.Run(ctx, "user:dave:fw", fwArgs(nextWindow))
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
	if !res.Allowed {
		t.Fatal("should be allowed after window reset")
	}
}

// --- 競合状態の検証 (go test -race) ---

func TestMemoryStorage_FixedWindow_Race(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()
	now := int64(1_000_000)

	const goroutines = 100
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := s.Run(ctx, "user:race:fw", fwArgs(now))
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() > fwLimit {
		t.Fatalf("allowed %d requests, but limit is %d — mutex broken", allowed.Load(), fwLimit)
	}
}

// ========================
// Sliding Window Counter
// ========================

func TestMemoryStorage_SlidingWindowCounter_AllowsUpToLimit(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()

	for i := 0; i < int(swcLimit); i++ {
		res, err := s.Run(ctx, "user:alice:swc", swcArgs(swcNowMs))
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	res, err := s.Run(ctx, "user:alice:swc", swcArgs(swcNowMs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("request over limit should be denied")
	}
	if res.ResetMS <= 0 {
		t.Fatal("ResetMS should be positive when denied")
	}
}

func TestMemoryStorage_SlidingWindowCounter_DifferentKeysAreIndependent(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()

	for i := 0; i < int(swcLimit); i++ {
		s.Run(ctx, "user:alice:swc", swcArgs(swcNowMs))
	}

	res, err := s.Run(ctx, "user:bob:swc", swcArgs(swcNowMs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("bob should not be affected by alice's window")
	}
}

func TestMemoryStorage_SlidingWindowCounter_RemainingDecreases(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()

	prev := int(swcLimit)
	for i := 0; i < int(swcLimit); i++ {
		res, _ := s.Run(ctx, "user:carol:swc", swcArgs(swcNowMs))
		if res.Remaining >= prev {
			t.Fatalf("remaining should decrease: got %d, prev %d", res.Remaining, prev)
		}
		prev = res.Remaining
	}
}

func TestMemoryStorage_SlidingWindowCounter_ResetsAfterWindow(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()

	for i := 0; i < int(swcLimit); i++ {
		s.Run(ctx, "user:dave:swc", swcArgs(swcNowMs))
	}

	res, _ := s.Run(ctx, "user:dave:swc", swcArgs(swcNowMs))
	if res.Allowed {
		t.Fatal("should be denied before window expires")
	}

	// 2ウィンドウ先に進める: prevCountも0になり完全リセット
	twoWindowsLater := swcNowMs + swcWindowSize.Milliseconds()*2
	res, err := s.Run(ctx, "user:dave:swc", swcArgs(twoWindowsLater))
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
	if !res.Allowed {
		t.Fatal("should be allowed after window reset")
	}
}

// 線形補間が正しく機能するかを検証:
// 前ウィンドウで4件送信 → 40s経過後 (overlap_rate≈0.333) は estimated=1 となり
// 現在ウィンドウで4件許可、5件目は拒否されることを確認する
func TestMemoryStorage_SlidingWindowCounter_LinearInterpolation(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()

	// 前ウィンドウ (900s～960s) の30s地点で4件送信 → curr_window_start=900_000ms
	prevWindowNowMs := int64(930_000)
	for i := 0; i < 4; i++ {
		res, _ := s.Run(ctx, "user:eve:swc", swcArgs(prevWindowNowMs))
		if !res.Allowed {
			t.Fatalf("prev window request %d should be allowed", i+1)
		}
	}

	// swcNowMs=1_000_000 (現在ウィンドウ960sの40s地点):
	//   overlap_rate = 1 - 40/60 ≈ 0.333
	//   estimated   = floor(4 * 0.333 + curr_count)
	// curr_count が 0,1,2,3 のときは estimated=1,2,3,4 < limit=5 → 許可
	// curr_count が 4 のときは estimated=floor(1.333+4)=5 >= limit=5 → 拒否
	for i := 0; i < 4; i++ {
		res, err := s.Run(ctx, "user:eve:swc", swcArgs(swcNowMs))
		if err != nil {
			t.Fatalf("curr window request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("curr window request %d should be allowed (prev contribution ≈1)", i+1)
		}
	}

	res, _ := s.Run(ctx, "user:eve:swc", swcArgs(swcNowMs))
	if res.Allowed {
		t.Fatal("5th curr window request should be denied due to prev window contribution")
	}
}

// --- 競合状態の検証 (go test -race) ---

func TestMemoryStorage_SlidingWindowCounter_Race(t *testing.T) {
	s := storage.NewMemoryStorage()
	ctx := context.Background()

	const goroutines = 100
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := s.Run(ctx, "user:race:swc", swcArgs(swcNowMs))
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() > swcLimit {
		t.Fatalf("allowed %d requests, but limit is %d — mutex broken", allowed.Load(), swcLimit)
	}
}
