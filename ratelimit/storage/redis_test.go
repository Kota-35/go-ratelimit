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
