package ratelimit_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go-ratelimit/ratelimit"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// --- MemoryStoreMutex ---

// 1 goroutine のシングルスレッドベースライン
func BenchmarkMemoryStoreMutex_Allow(b *testing.B) {
	store := ratelimit.NewMemoryStoreMutex()
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: float64(b.N + 1), RefillRate: 1e9}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Allow(ctx, "bench:user", cfg)
	}
}

// b.RunParallel で GOMAXPROCS 分の goroutine を走らせ mutex 競合を計測
func BenchmarkMemoryStoreMutex_AllowParallel(b *testing.B) {
	store := ratelimit.NewMemoryStoreMutex()
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			store.Allow(ctx, "bench:user", cfg)
		}
	})
}

// 　ユーザーが増えた時
func BenchmarkMemoryStoreMutex_AllowParallelMultiUser(b *testing.B) {
	store := ratelimit.NewMemoryStoreMutex()
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}
	var n atomic.Int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := fmt.Sprintf("bench:user:%d", n.Add(1))
		for pb.Next() {
			store.Allow(ctx, key, cfg)
		}
	})
}

// --- MemoryStoreSyncMap

// 1 goroutine のシングルスレッドベースライン
func BenchmarkMemoryStoreSyncMap_Allow(b *testing.B) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: float64(b.N + 1), RefillRate: 1e9}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Allow(ctx, "bench:user", cfg)
	}
}

// b.RunParallel で GOMAXPROCS 分の goroutine を走らせ mutex 競合を計測
func BenchmarkMemoryStoreSyncMap_AllowParallel(b *testing.B) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			store.Allow(ctx, "bench:user", cfg)
		}
	})
}

// 　ユーザーが増えた時
func BenchmarkMemoryStoreSyncMap_AllowParallelMultiUser(b *testing.B) {
	store := ratelimit.NewMemoryStoreSyncMap()
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}
	var n atomic.Int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := fmt.Sprintf("bench:user:%d", n.Add(1))
		for pb.Next() {
			store.Allow(ctx, key, cfg)
		}
	})
}

// --- Redis ---

func BenchmarkRedisStore_Allow(b *testing.B) {
	mr := miniredis.RunT(b)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := ratelimit.NewRedisStore(rdb)
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Allow(ctx, "bench:user", cfg)
	}
}

func BenchmarkRedisStore_AllowParallel(b *testing.B) {
	mr := miniredis.RunT(b)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := ratelimit.NewRedisStore(rdb)
	ctx := context.Background()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			store.Allow(ctx, "bench:user", cfg)
		}
	})
}

// ─── HTTP ミドルウェア (エンドツーエンド) ─────────────────────

func BenchmarkMiddleware_Allow(b *testing.B) {
	store := ratelimit.NewMemoryStoreMutex()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}

	handler := ratelimit.NewMiddleware(store, cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-User-ID", "bench")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)
	}
}

func BenchmarkMiddleware_AllowParallel(b *testing.B) {
	store := ratelimit.NewMemoryStoreMutex()
	cfg := ratelimit.Config{Capacity: 1e12, RefillRate: 1e9}

	handler := ratelimit.NewMiddleware(store, cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("X-User-ID", "bench")
		for pb.Next() {
			rw := httptest.NewRecorder()
			handler.ServeHTTP(rw, req)
		}
	})
}
