package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

type bucketStoreSyncMap struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

type MemoryStoreSyncMap struct {
	m sync.Map
}

func NewMemoryStoreSyncMap() *MemoryStoreSyncMap {
	return &MemoryStoreSyncMap{}
}

func (s *MemoryStoreSyncMap) Allow(_ context.Context, key string, cfg Config) (Result, error) {
	c, ok := cfg.(TokenBucketConfig)
	if !ok {
		return Result{}, fmt.Errorf("FixedWindowStore: got %T, want FixedWindowConfig", cfg)
	}
	now := time.Now()

	actual, _ := s.m.LoadOrStore(key, &bucketStoreSyncMap{tokens: c.Capacity, lastRefill: now})
	b := actual.(*bucketStoreSyncMap)
	b.mu.Lock()
	defer b.mu.Unlock()

	// 経過時間 x refillRate でトークンを補充し, Capacity でキャップ
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = math.Min(c.Capacity, b.tokens+elapsed*c.RefillRate)
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens--
		return Result{
			Allowed:   true,
			Remaining: int(b.tokens),
			ResetMs:   0,
		}, nil
	}

	// 次の1トークンが補充されるまでのミリ秒を計算
	// 単位に注目!!
	// 1/refill_rate で 1トークンあたりの補充時間
	// => (1.0 - tokens)/refill_rate は 次のトークンまでの秒数
	resetMs := int64(math.Ceil((1.0 - b.tokens) / c.RefillRate * 1000))
	return Result{
		Allowed:   false,
		Remaining: 0,
		ResetMs:   resetMs,
	}, nil
}
