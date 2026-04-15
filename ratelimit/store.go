package ratelimit

import "context"

type Result struct {
	Allowed   bool
	Remaining int
	ResetMs   int64 // 次オントークンが補充されるまでのミリ秒
}

type Store interface {
	Allow(ctx context.Context, key string, cfg Config) (Result, error)
}
