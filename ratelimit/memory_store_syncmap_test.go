package ratelimit_test

import (
	"context"
	"go-ratelimit/ratelimit"
	"sync"
	"sync/atomic"
	"testing"
)

// var testCfg = ratelimit.Config{Capacity: 5, RefillRate: 1}

func TestMemoryStoreSyncMap_AllowesUpToCapacity(t *testing.T) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()
	key := "user:alice:rl"

	for i := 0; i < int(testCfg.Capacity); i++ {
		res, err := store.Allow(ctx, key, testCfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// 6回目は拒否されるべき
	res, err := store.Allow(ctx, key, testCfg)
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

func TestMemoryStoreSyncMap_DifferentKeysAreIndependent(t *testing.T) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()

	key1 := "user:alice:rl"
	key2 := "user:bob:rl"

	// key1 のバケツを使い切る
	for i := 0; i < int(testCfg.Capacity); i++ {
		store.Allow(ctx, key1, testCfg)
	}

	// key2 は影響を受けない
	res, err := store.Allow(ctx, key2, testCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("key2 should not be affected by key1's bucket")
	}
}

func TestMemoryStoreSyncMap_RemainingDecreases(t *testing.T) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()
	key := "user:carol:rl"

	prev := int(testCfg.Capacity)
	for i := 0; i < int(testCfg.Capacity); i++ {
		res, _ := store.Allow(ctx, key, testCfg)
		if res.Remaining >= prev {
			t.Fatalf("remaining should be decrease: get %d, prev %d", res.Remaining, prev)
		}
		prev = res.Remaining
	}
}

func TestMemoryStoreSyncMap_Race(t *testing.T) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()
	key := "user:rate:rl"

	const goroutines = 100
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := store.Allow(ctx, key, testCfg)
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// 許可数はCapacityを超えてはいけない
	if allowed.Load() > int64(testCfg.Capacity) {
		t.Fatalf("allowed %d requests, but capacity is %v", allowed.Load(), testCfg.Capacity)
	}
}
