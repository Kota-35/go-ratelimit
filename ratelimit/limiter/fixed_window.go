package limiter

import (
	"context"
	"fmt"
	"go-ratelimit/ratelimit/storage"
	"time"
)

type FixedWindowConfig struct {
	Limit      int64
	WindowSize time.Duration
}

type FixedWindowLimiter struct {
	store  storage.Storage
	config FixedWindowConfig
}

func NewFixedWindowLimiter(store storage.Storage, cfg FixedWindowConfig) *FixedWindowLimiter {
	return &FixedWindowLimiter{store: store, config: cfg}
}

func (l *FixedWindowLimiter) Allow(ctx context.Context, key string) (Result, error) {
	res, err := l.store.Run(ctx, key, storage.FixedWindowArgs{
		Limit:      l.config.Limit,
		WindowSize: l.config.WindowSize,
		NowMs:      time.Now().UnixMilli(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("fixed window: %w", err)
	}
	return storageResultToResult(res), nil
}
