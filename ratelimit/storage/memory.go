package storage

import (
	"context"
	"fmt"
	"math"
	"sync"
)

type tokenBucketState struct {
	tokens       float64
	lastRefillMs int64
}

type fixedWindowState struct {
	slot  int64 // NowMs / WindowSizeMs で決まるウィンドウ番号
	count int64
}

type slidingWindowCounterState struct {
	currWindowStart int64 // 現在ウィンドウ開始時刻 (ms)
	prevCount       int64
	currCount       int64
}

type MemoryStorage struct {
	mu    sync.Mutex
	state map[string]any // アルゴリズムごとに異なる状態を保持するため
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		state: make(map[string]any),
	}
}

func (s *MemoryStorage) Run(ctx context.Context, key string, args RunArgs) (LimiterResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch a := args.(type) {
	case TokenBucketArgs:
		return s.runTokenBucket(key, a)
	case FixedWindowArgs:
		return s.runFixedWindow(key, a)
	case SlidingWindowCounterArgs:
		return s.runSlidingWindowCounter(key, a)
	default:
		return LimiterResult{}, fmt.Errorf("unsupported args type: %T", args)
	}
}

func (s *MemoryStorage) runTokenBucket(key string, args TokenBucketArgs) (LimiterResult, error) {
	raw, ok := s.state[key]
	var st *tokenBucketState
	if !ok {
		// 初回: バケツ満タンで登録
		st = &tokenBucketState{tokens: args.Capacity, lastRefillMs: args.NowMs}
		s.state[key] = st
	} else {
		st = raw.(*tokenBucketState)
	}

	// 経過時間 × refillRate でトークンを補充し Capacity でキャップ
	elapsed := float64(args.NowMs-st.lastRefillMs) / 1000.0
	st.tokens = math.Min(args.Capacity, st.tokens+elapsed*args.RefillRate)
	st.lastRefillMs = args.NowMs

	if st.tokens >= 1.0 {
		st.tokens--
		return LimiterResult{
			Allowed:   true,
			Remaining: int(math.Floor(st.tokens)),
			ResetMS:   0,
		}, nil
	}

	// 次の1トークンが補充されるまでのミリ秒
	resetMs := int64(math.Ceil((1.0 - st.tokens) / args.RefillRate * 1000))
	return LimiterResult{
		Allowed:   false,
		Remaining: 0,
		ResetMS:   resetMs,
	}, nil
}

func (s *MemoryStorage) runSlidingWindowCounter(key string, args SlidingWindowCounterArgs) (LimiterResult, error) {
	windowMs  := args.WindowSize.Milliseconds()
	windowSec := int64(args.WindowSize.Seconds())
	nowSec    := args.NowMs / 1000

	// 現在ウィンドウ開始時刻 (ms) — Luaの curr_window_start * 1000 と同じ計算
	currWindowStartMs := (nowSec / windowSec) * windowSec * 1000

	raw, ok := s.state[key]
	var st *slidingWindowCounterState
	if !ok {
		st = &slidingWindowCounterState{currWindowStart: currWindowStartMs}
		s.state[key] = st
	} else {
		st = raw.(*slidingWindowCounterState)
		if currWindowStartMs > st.currWindowStart {
			if currWindowStartMs == st.currWindowStart+windowMs {
				// 1つ先のウィンドウ: 現在→前にシフト
				st.prevCount = st.currCount
			} else {
				// 2つ以上先: 前ウィンドウも参照不要
				st.prevCount = 0
			}
			st.currCount = 0
			st.currWindowStart = currWindowStartMs
		}
	}

	elapsedMs   := args.NowMs - currWindowStartMs
	overlapRate := 1.0 - float64(elapsedMs)/float64(windowMs)
	estimated   := int64(math.Floor(float64(st.prevCount)*overlapRate + float64(st.currCount)))
	resetMs     := currWindowStartMs + windowMs - args.NowMs

	if estimated >= args.Limit {
		return LimiterResult{Allowed: false, Remaining: 0, ResetMS: resetMs}, nil
	}

	st.currCount++
	return LimiterResult{
		Allowed:   true,
		Remaining: int(args.Limit - estimated - 1),
		ResetMS:   0,
	}, nil
}

func (s *MemoryStorage) runFixedWindow(key string, args FixedWindowArgs) (LimiterResult, error) {
	windowMs := args.WindowSize.Milliseconds()
	slot := args.NowMs / windowMs

	raw, ok := s.state[key]
	var st *fixedWindowState
	if !ok || raw.(*fixedWindowState).slot != slot {
		// 初回 or ウィンドウが切り替わった
		st = &fixedWindowState{slot: slot, count: 0}
		s.state[key] = st
	} else {
		st = raw.(*fixedWindowState)
	}

	// 次のウィンドウ開始までのミリ秒
	resetMs := (slot+1)*windowMs - args.NowMs

	if st.count < args.Limit {
		st.count++
		return LimiterResult{
			Allowed:   true,
			Remaining: int(args.Limit - st.count),
			ResetMS:   0,
		}, nil
	}

	return LimiterResult{
		Allowed:   false,
		Remaining: 0,
		ResetMS:   resetMs,
	}, nil
}
