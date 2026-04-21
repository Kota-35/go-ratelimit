package storage

import (
	"context"
	"time"
)

// `ResetMs` は次にりクエストを通せる最短時間(ms)
type LimiterResult struct {
	Allowed   bool
	Remaining int
	ResetMS   int64
}

type Storage interface {
	Run(ctx context.Context, key string, args RunArgs) (LimiterResult, error)
}

type RunArgs interface {
	runArgsTag()
}

type FixedWindowArgs struct {
	Limit      int64
	WindowSize time.Duration
	NowMs      int64
}

func (FixedWindowArgs) runArgsTag() {}

type SlidingWindowCounterArgs struct {
	Limit      int64
	WindowSize time.Duration
	NowMs      int64
}

func (SlidingWindowCounterArgs) runArgsTag() {}

type TokenBucketArgs struct {
	Capacity   float64
	RefillRate float64
	NowMs      int64
}

func (TokenBucketArgs) runArgsTag() {}

