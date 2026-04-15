package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"go-ratelimit/ratelimit"
)

var testCfg = ratelimit.Config{Capacity: 5, RefillRate: 1}

// --- 基本動作 ---

func TestMemoryStore_AllowsUpToCapacity(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < int(testCfg.Capacity); i++ {
		res, err := store.Allow(ctx, "user:alice:rl", testCfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// 6回目は拒否されるべき
	res, err := store.Allow(ctx, "user:alice:rl", testCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("6th request should be denied")
	}
	if res.ResetMs <= 0 {
		t.Fatal("ResetMs should be positive when denied")
	}
}

func TestMemoryStore_DifferentKeysAreIndependent(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	ctx := context.Background()

	// alice のバケツを使い切る
	for i := 0; i < int(testCfg.Capacity); i++ {
		store.Allow(ctx, "user:alice:rl", testCfg) //nolint
	}

	// bob は影響を受けない
	res, err := store.Allow(ctx, "user:bob:rl", testCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("bob should not be affected by alice's bucket")
	}
}

func TestMemoryStore_RemainingDecreases(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	ctx := context.Background()

	prev := int(testCfg.Capacity)
	for i := 0; i < int(testCfg.Capacity); i++ {
		res, _ := store.Allow(ctx, "user:carol:rl", testCfg)
		if res.Remaining >= prev {
			t.Fatalf("remaining should decrease: got %d, prev %d", res.Remaining, prev)
		}
		prev = res.Remaining
	}
}

// --- 競合状態の検証 (go test -race) ---

func TestMemoryStore_Race(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	ctx := context.Background()

	const goroutines = 100
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := store.Allow(ctx, "user:race:rl", testCfg)
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// 許可数は capacity を超えてはいけない
	if allowed.Load() > int64(testCfg.Capacity) {
		t.Fatalf("allowed %d requests, but capacity is %v", allowed.Load(), testCfg.Capacity)
	}
}
