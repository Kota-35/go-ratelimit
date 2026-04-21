package limiter_test

import (
	"context"
	"go-ratelimit/ratelimit/limiter"
	"go-ratelimit/ratelimit/storage"
	"testing"
	"time"
)

var testSWCCfg = limiter.SlidingWindowCounterConfig{
	Limit:      5,
	WindowSize: 60 * time.Second,
}

// --- args の組み立て検証 ---

func TestSlidingWindowCounterLimiter_PassesCorrectArgs(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: true}}
	l := limiter.NewSlidingWindowCounterConfig(mock, testSWCCfg)

	before := time.Now().UnixMilli()
	l.Allow(context.Background(), "user:alice:swc")
	after := time.Now().UnixMilli()

	args, ok := mock.gotArgs.(storage.SlidingWindowCounterArgs)
	if !ok {
		t.Fatalf("expected SlidingWindowCounterArgs, got %T", mock.gotArgs)
	}
	if args.Limit != testSWCCfg.Limit {
		t.Errorf("Limit: got %v, want %v", args.Limit, testSWCCfg.Limit)
	}
	if args.WindowSize != testSWCCfg.WindowSize {
		t.Errorf("WindowSize: got %v, want %v", args.WindowSize, testSWCCfg.WindowSize)
	}
	if args.NowMs < before || args.NowMs > after {
		t.Errorf("NowMs %d is out of expected range [%d, %d]", args.NowMs, before, after)
	}

}

// --- 結果のマッピング検証 ---

func TestSlidingWindowCounterLImiter_MapsAllowedResult(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: true, Remaining: 4, ResetMS: 0}}
	l := limiter.NewSlidingWindowCounterConfig(mock, testSWCCfg)

	res, err := l.Allow(context.Background(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("Allowed: got false, want true")
	}
	if res.Remaining != 4 {
		t.Errorf("Remaining: got %d, want 4", res.Remaining)
	}
	if res.ResetMs != 0 {
		t.Errorf("ResetMs: got %d, want 0", res.ResetMs)
	}
}

func TestSlidingWindowCounterLimiter_MapsDeniedResult(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: false, Remaining: 0, ResetMS: 30_000}}
	l := limiter.NewSlidingWindowCounterConfig(mock, testSWCCfg)

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
	if res.ResetMs != 30_000 {
		t.Errorf("ResetMs: got %d, want 30000", res.ResetMs)
	}
}
