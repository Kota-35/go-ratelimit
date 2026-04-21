package limiter_test

import (
	"context"
	"testing"
	"time"

	"go-ratelimit/ratelimit/limiter"
	"go-ratelimit/ratelimit/storage"
)

type mockStorage struct {
	gotKey  string
	gotArgs storage.RunArgs
	ret     storage.LimiterResult
}

func (m *mockStorage) Run(_ context.Context, key string, args storage.RunArgs) (storage.LimiterResult, error) {
	m.gotKey = key
	m.gotArgs = args
	return m.ret, nil
}

var testCfg = limiter.TokenBucketConfig{Capacity: 5, RefillRate: 2}

// --- args の組み立て検証 ---

func TestTokenBucketLimiter_PassesCorrectArgs(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: true}}
	l := limiter.NewTokenBucketLimiter(mock, testCfg)

	before := time.Now().UnixMilli()
	l.Allow(context.Background(), "user:alice:rl")
	after := time.Now().UnixMilli()

	args, ok := mock.gotArgs.(storage.TokenBucketArgs)
	if !ok {
		t.Fatalf("expected TokenBucketArgs, got %T", mock.gotArgs)
	}
	if args.Capacity != testCfg.Capacity {
		t.Errorf("Capacity: got %v, want %v", args.Capacity, testCfg.Capacity)
	}
	if args.RefillRate != testCfg.RefillRate {
		t.Errorf("RefillRate: got %v, want %v", args.RefillRate, testCfg.RefillRate)
	}
	if args.NowMs < before || args.NowMs > after {
		t.Errorf("NowMs %d is out of expected range [%d, %d]", args.NowMs, before, after)
	}
}

// --- 結果のマッピング検証 ---

func TestTokenBucketLimiter_MapsAllowedResult(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: true, Remaining: 3, ResetMS: 0}}
	l := limiter.NewTokenBucketLimiter(mock, testCfg)

	res, err := l.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("Allowed: got false, want true")
	}
	if res.Remaining != 3 {
		t.Errorf("Remaining: got %d, want 3", res.Remaining)
	}
	if res.ResetMs != 0 {
		t.Errorf("ResetMs: got %d, want 0", res.ResetMs)
	}
}

func TestTokenBucketLimiter_MapsDeniedResult(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: false, Remaining: 0, ResetMS: 500}}
	l := limiter.NewTokenBucketLimiter(mock, testCfg)

	res, err := l.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Error("Allowed: got true, want false")
	}
	if res.Remaining != 0 {
		t.Errorf("Remaining: got %d, want 0", res.Remaining)
	}
	if res.ResetMs != 500 {
		t.Errorf("ResetMs: got %d, want 500", res.ResetMs)
	}
}
