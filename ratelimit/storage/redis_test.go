package storage_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-ratelimit/ratelimit/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedisStorage(t *testing.T) (*storage.RedisStorage, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return storage.NewRedisStorage(rdb), mr
}

// --- 基本動作 ---

// ========================
// Token Bucket
// ========================

func TestRedisStorage_TokenBucket_AllowsUpToCapacity(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

func TestRedisStorage_TokenBucket_DifferentKeysAreIndependent(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

func TestRedisStorage_TokenBucket_RemainingDecreases(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

// Lua スクリプトが ARGV[3] で時刻を受け取るため、NowMs を注入すれば
// time.Sleep なしでリフィルを検証できる

func TestRedisStorage_TokenBucket_RefillOverTime(t *testing.T) {
	s, _ := newTestRedisStorage(t)
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < capacity; i++ {
		s.Run(ctx, "user:dave:rl", baseArgs(now))
	}

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

// 複数 goroutine から同時アクセスしても、Lua スクリプトが atomic に実行されるため
// 許可数が capacity を超えないことを確認する

func TestRedisStorage_TokenBucket_AtomicUnderConcurrency(t *testing.T) {
	s, _ := newTestRedisStorage(t)
	ctx := context.Background()
	now := int64(1_000_000)

	const goroutines = 50
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
		t.Fatalf("allowed %d requests, but capacity is %d — Lua atomic broken", allowed.Load(), capacity)
	}
}

// ========================
// Fixed Window
// ========================

func TestRedisStorage_FixedWindow_AllowsUpToLimit(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

func TestRedisStorage_FixedWindow_DifferentKeysAreIndependent(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

func TestRedisStorage_FixedWindow_RemainingDecreases(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

// Redis の固定ウィンドウは TTL (EXPIRE/PTTL) でウィンドウを管理するため、
// miniredis の FastForward でリセットを検証する

func TestRedisStorage_FixedWindow_ResetsAfterWindow(t *testing.T) {
	s, mr := newTestRedisStorage(t)
	ctx := context.Background()
	now := int64(1_000_000)

	for i := 0; i < int(fwLimit); i++ {
		s.Run(ctx, "user:dave:fw", fwArgs(now))
	}

	res, _ := s.Run(ctx, "user:dave:fw", fwArgs(now))
	if res.Allowed {
		t.Fatal("should be denied before window expires")
	}

	mr.FastForward(fwWindowSize + time.Second)

	res, err := s.Run(ctx, "user:dave:fw", fwArgs(now))
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
	if !res.Allowed {
		t.Fatal("should be allowed after window reset")
	}
}

// ========================
// Sliding Window Counter
// ========================

func TestRedisStorage_SlidingWindowCounter_AllowsUpToLimit(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

func TestRedisStorage_SlidingWindowCounter_DifferentKeysAreIndependent(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

func TestRedisStorage_SlidingWindowCounter_RemainingDecreases(t *testing.T) {
	s, _ := newTestRedisStorage(t)
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

// Luaスクリプトは ARGV[3] の now_ms でウィンドウを決定するため
// now_ms を2ウィンドウ分進めるだけでリセットを検証できる（FastForward不要）
func TestRedisStorage_SlidingWindowCounter_ResetsAfterWindow(t *testing.T) {
	s, _ := newTestRedisStorage(t)
	ctx := context.Background()

	for i := 0; i < int(swcLimit); i++ {
		s.Run(ctx, "user:dave:swc", swcArgs(swcNowMs))
	}

	res, _ := s.Run(ctx, "user:dave:swc", swcArgs(swcNowMs))
	if res.Allowed {
		t.Fatal("should be denied before window expires")
	}

	// 2ウィンドウ先: prev_key・curr_key が両方とも未使用のキーになる
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
func TestRedisStorage_SlidingWindowCounter_LinearInterpolation(t *testing.T) {
	s, _ := newTestRedisStorage(t)
	ctx := context.Background()

	// 前ウィンドウ (900s～960s) の30s地点で4件送信
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

func TestRedisStorage_SlidingWindowCounter_AtomicUnderConcurrency(t *testing.T) {
	s, _ := newTestRedisStorage(t)
	ctx := context.Background()

	const goroutines = 50
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
		t.Fatalf("allowed %d requests, but limit is %d — Lua atomic broken", allowed.Load(), swcLimit)
	}
}

func TestRedisStorage_FixedWindow_AtomicUnderConcurrency(t *testing.T) {
	s, _ := newTestRedisStorage(t)
	ctx := context.Background()
	now := int64(1_000_000)

	const goroutines = 50
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
		t.Fatalf("allowed %d requests, but limit is %d — Lua atomic broken", allowed.Load(), fwLimit)
	}
}
