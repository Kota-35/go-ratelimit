package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

type bucketStateMutex struct {
	tokens     float64
	lastRefill time.Time
}

type MemoryStoreMutex struct {
	mu      sync.Mutex
	buckets map[string]*bucketStateMutex
}

func NewMemoryStoreMutex() *MemoryStoreMutex {
	return &MemoryStoreMutex{
		buckets: make(map[string]*bucketStateMutex),
	}
}

func (s *MemoryStoreMutex) Allow(_ context.Context, key string, cfg Config) (Result, error) {
	c, ok := cfg.(TokenBucketConfig)
	if !ok {
		return Result{}, fmt.Errorf("FixedWindowStore: got %T, want FixedWindowConfig", cfg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	b, ok := s.buckets[key]
	if !ok {
		// 初回: バケツ満タンで登録
		b = &bucketStateMutex{tokens: c.Capacity, lastRefill: now}
		s.buckets[key] = b
	}

	// 経過時間 × refillRate でトークンを補充し Capacity でキャップ
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
