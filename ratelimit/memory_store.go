package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

type bucketState struct {
	tokens     float64
	lastRefill time.Time
}

type MemoryStore struct {
	mu      sync.Mutex
	buckets map[string]*bucketState
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		buckets: make(map[string]*bucketState),
	}
}

func (s *MemoryStore) Allow(_ context.Context, key string, cfg Config) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	b, ok := s.buckets[key]
	if !ok {
		// 初回: バケツ満タンで登録
		b = &bucketState{tokens: cfg.Capacity, lastRefill: now}
		s.buckets[key] = b
	}

	// 経過時間 × refillRate でトークンを補充し Capacity でキャップ
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = math.Min(cfg.Capacity, b.tokens+elapsed*cfg.RefillRate)
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens--
		return Result{
			Allowed:   true,
			Remaining: int(b.tokens),
			ResetMs:   0,
		}, nil
	}

	// 次の1トークンが補充されるまでのミリ秒
	resetMs := int64(math.Ceil((1.0-b.tokens)/cfg.RefillRate*1000))
	return Result{
		Allowed:   false,
		Remaining: 0,
		ResetMs:   resetMs,
	}, nil
}
