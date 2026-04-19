package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"go-ratelimit/ratelimit"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedisStore(t *testing.T) (*ratelimit.RedisStoreTokenBucket, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return ratelimit.NewRedisStoreTokenBucket(rdb), mr
}

// --- 基本動作 ---

func TestRedisStore_AllowsUpToCapacity(t *testing.T) {
	store, _ := newTestRedisStore(t)
	ctx := context.Background()

	for i := 0; i < int(testCfg.Capacity); i++ {
		res, err := store.Allow(ctx, "user:alice:rl", testCfg)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

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

func TestRedisStore_DifferentKeysAreIndependent(t *testing.T) {
	store, _ := newTestRedisStore(t)
	ctx := context.Background()

	for i := 0; i < int(testCfg.Capacity); i++ {
		store.Allow(ctx, "user:alice:rl", testCfg)
	}

	res, err := store.Allow(ctx, "user:bob:rl", testCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("bob should not be affected by alice's bucket")
	}
}

// --- Lua atomic の検証: 複数 goroutine からの同時アクセスで capacity を超えないか ---

func TestRedisStore_AtomicUnderConcurrency(t *testing.T) {
	store, _ := newTestRedisStore(t)
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := store.Allow(ctx, "user:concurrent:rl", testCfg)
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() > int64(testCfg.Capacity) {
		t.Fatalf("allowed %d requests, but capacity is %v — Lua atomic broken", allowed.Load(), testCfg.Capacity)
	}
}
