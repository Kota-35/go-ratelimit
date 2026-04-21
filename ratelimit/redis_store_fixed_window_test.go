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

var fwCfg = ratelimit.FixedWindowConfig{Limit: 5, WindowSecs: 60}

func newTestFixedWindowStore(t *testing.T) (*ratelimit.RedisStoreFixedWindow, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return ratelimit.NewRedisStoreFixedWindow(rdb), mr
}

func TestFixedWindow_AllowsUpToLimit(t *testing.T) {
	store, _ := newTestFixedWindowStore(t)
	ctx := context.Background()

	for i := 0; i < fwCfg.Limit; i++ {
		res, err := store.Allow(ctx, "user:alice:fw", fwCfg)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	res, err := store.Allow(ctx, "user:alice:fw", fwCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("request over limit should be denied")
	}
	if res.ResetMs <= 0 {
		t.Fatal("ResetMs should be positive when denied")
	}
}

func TestFixedWindow_DifferentKeysAreIndependent(t *testing.T) {
	store, _ := newTestFixedWindowStore(t)
	ctx := context.Background()

	for i := 0; i < fwCfg.Limit; i++ {
		store.Allow(ctx, "user:alice:fw", fwCfg)
	}

	res, err := store.Allow(ctx, "user:bob:fw", fwCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("bob should not be affected by alice's window")
	}
}

func TestFixedWindow_ResetsAfterWindow(t *testing.T) {
	store, mr := newTestFixedWindowStore(t)
	ctx := context.Background()

	for i := 0; i < fwCfg.Limit; i++ {
		store.Allow(ctx, "user:alice:fw", fwCfg)
	}

	res, _ := store.Allow(ctx, "user:alice:fw", fwCfg)
	if res.Allowed {
		t.Fatal("should be denied before window expires")
	}

	// miniredis で時刻を進めてウィンドウをリセット
	mr.FastForward(61e9) // 61秒

	res, err := store.Allow(ctx, "user:alice:fw", fwCfg)
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
	if !res.Allowed {
		t.Fatal("should be allowed after window reset")
	}
}

func TestFixedWindow_AtomicUnderConcurrency(t *testing.T) {
	store, _ := newTestFixedWindowStore(t)
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	var allowed atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := store.Allow(ctx, "user:concurrent:fw", fwCfg)
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() > int64(fwCfg.Limit) {
		t.Fatalf("allowed %d requests, but limit is %d — Lua atomic broken", allowed.Load(), fwCfg.Limit)
	}
}
