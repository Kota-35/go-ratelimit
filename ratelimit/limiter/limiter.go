package limiter

import (
	"context"
	"go-ratelimit/ratelimit/storage"
)

type Result struct {
	Allowed   bool
	Remaining int
	ResetMs   int64
}

type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// storage層の結果をlimiter層の結果に変換
func storageResultToResult(r storage.LimiterResult) Result {
	return Result{
		Allowed:   r.Allowed,
		Remaining: r.Remaining,
		ResetMs:   r.ResetMS,
	}
}
