package ratelimit

import "context"

// Allowの結果
//
// `ResetMs` は次にりクエストを通せる最短時間(ms)
type Result struct {
	Allowed   bool
	Remaining int
	ResetMs   int64 // 次オントークンが補充されるまでのミリ秒
}

type Store interface {
	Allow(ctx context.Context, key string, cfg Config) (Result, error)
}
