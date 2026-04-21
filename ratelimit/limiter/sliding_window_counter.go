package limiter

import (
	"context"
	"fmt"
	"go-ratelimit/ratelimit/storage"
	"time"
)

type SlidingWindowCounterConfig struct {
	Limit      int64
	WindowSize time.Duration
}
type SlidingWindowCounterLimiter struct {
	store  storage.Storage
	config SlidingWindowCounterConfig
}

func NewSlidingWindowCounterConfig(store storage.Storage, cfg SlidingWindowCounterConfig) *SlidingWindowCounterLimiter {
	return &SlidingWindowCounterLimiter{
		store:  store,
		config: cfg,
	}
}

func (l *SlidingWindowCounterLimiter) Allow(ctx context.Context, key string) (Result, error) {
	res, err := l.store.Run(ctx, key, storage.SlidingWindowCounterArgs{
		Limit:      l.config.Limit,
		WindowSize: l.config.WindowSize,
		NowMs:      time.Now().UnixMilli(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("sliding window counter: %w", err)
	}
	return storageResultToResult(res), nil
}
