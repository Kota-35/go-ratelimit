package limiter_test

import (
	"context"
	"testing"
	"time"

	"go-ratelimit/ratelimit/limiter"
	"go-ratelimit/ratelimit/storage"
)

var testFWCfg = limiter.FixedWindowConfig{
	Limit:      5,
	WindowSize: 60 * time.Second,
}

// --- args の組み立て検証 ---

func TestFixedWindowLimiter_PassesCorrectArgs(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: true}}
	l := limiter.NewFixedWindowLimiter(mock, testFWCfg)

	before := time.Now().UnixMilli()
	l.Allow(context.Background(), "user:alice:fw")
	after := time.Now().UnixMilli()

	args, ok := mock.gotArgs.(storage.FixedWindowArgs)
	if !ok {
		t.Fatalf("expected FixedWindowArgs, got %T", mock.gotArgs)
	}
	if args.Limit != testFWCfg.Limit {
		t.Errorf("Limit: got %v, want %v", args.Limit, testFWCfg.Limit)
	}
	if args.WindowSize != testFWCfg.WindowSize {
		t.Errorf("WindowSize: got %v, want %v", args.WindowSize, testFWCfg.WindowSize)
	}
	if args.NowMs < before || args.NowMs > after {
		t.Errorf("NowMs %d is out of expected range [%d, %d]", args.NowMs, before, after)
	}
}

// --- 結果のマッピング検証 ---

func TestFixedWindowLimiter_MapsAllowedResult(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: true, Remaining: 4, ResetMS: 0}}
	l := limiter.NewFixedWindowLimiter(mock, testFWCfg)

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

func TestFixedWindowLimiter_MapsDeniedResult(t *testing.T) {
	mock := &mockStorage{ret: storage.LimiterResult{Allowed: false, Remaining: 0, ResetMS: 30_000}}
	l := limiter.NewFixedWindowLimiter(mock, testFWCfg)

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
