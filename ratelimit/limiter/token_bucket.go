package limiter

import (
	"context"
	"fmt"
	"go-ratelimit/ratelimit/storage"
	"time"
)

type TokenBucketConfig struct {
	Capacity   float64
	RefillRate float64
}

type TokenBucketLimiter struct {
	store  storage.Storage
	config TokenBucketConfig
}

func NewTokenBucketLimiter(store storage.Storage, cfg TokenBucketConfig) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		store:  store,
		config: cfg,
	}
}

func (l *TokenBucketLimiter) Allow(ctx context.Context, key string) (Result, error) {
	res, err := l.store.Run(ctx, key, storage.TokenBucketArgs{
		Capacity:   l.config.Capacity,
		RefillRate: l.config.RefillRate,
		NowMs:      time.Now().UnixMilli(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("token bucket: %w", err)
	}
	return storageResultToResult(res), nil
}
