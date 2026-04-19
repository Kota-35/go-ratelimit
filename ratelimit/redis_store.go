package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// luaScript は GET → 計算 → SET を atomic に実行する Lua スクリプト。
//
// Why: Go側で HGET -> 計算 -> SET の3ステップに分けて書かずに Lua スクリプトにしているのか？
// Luaスクリプトにすると Redisがシングルスレッドで全行を中断なく実行するから
// ```
// 時刻T1: goroutine A が HGET -> tokens=1 を読む
// 時刻T2: goroutine B が HGET -> tokens=1 を読む // Aがまだ書いていない
// 時刻T3: A が計算 -> tokens-1=0 -> SET
// 時刻T4: B が計算 -> tokens-1=0 -> SET // 本来は拒否すべきなのに通過してしまう。
// ```
// これがTOCTOU(Time of Check to Time of Use)競合
// ref: https://ja.wikipedia.org/wiki/Time-of-check_to_time-of-use
//
// KEYS[1]  : ハッシュキー (例: "user:alice:rl")
// ARGV[1]  : capacity  (float)
// ARGV[2]  : refill_rate (tokens/sec, float)
// ARGV[3]  : 現在時刻 (Unix ミリ秒, integer)
//
// 戻り値: {allowed(0|1), remaining(int), reset_ms(int)}
const luaScript = `
local data = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill')
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- data[1]がnil = Redisにキーが存在しない = 初回リクエスト
-- tokens を capacity で一杯にするのは新規ユーザーは制限なしでスタートできるから
if not tokens then
    tokens = capacity
    last_refill = now
end

local elapsed = (now - last_refill) / 1000.0
tokens = math.min(capacity, tokens + elapsed * refill_rate)

local allowed = 0
local reset_ms = 0

if tokens >= 1.0 then
    tokens = tokens - 1.0
    allowed = 1
else
    reset_ms = math.ceil((1.0 - tokens) / refill_rate * 1000)
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'last_refill', now)
redis.call('EXPIRE', KEYS[1], 3600)

-- math.floor(tokens)
-- 精度のため tokens　は内部で少数で保持
-- しかし、残り何回りクエストできるかは整数で残したい(remaining)
-- math.ceilだと ex. 1.79 -> 2 になり加増に見せてしまうので floor
return {allowed, math.floor(tokens), reset_ms}
`

type RedisStore struct {
	client *redis.Client
	script *redis.Script
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		client: client,
		script: redis.NewScript(luaScript),
	}
}

func (s *RedisStore) Allow(ctx context.Context, key string, cfg Config) (Result, error) {
	now := time.Now().UnixMilli()

	// EVALSHA → キャッシュミス時は自動で EVAL にフォールバック
	res, err := s.script.Run(ctx, s.client,
		[]string{key},
		cfg.Capacity, cfg.RefillRate, now,
	).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	allowed := res[0] == 1
	remaining := int(res[1])
	resetMs := res[2]

	if !allowed {
		return Result{
			Allowed:   false,
			Remaining: 0,
			ResetMs:   resetMs,
		}, nil
	}
	return Result{
		Allowed:   true,
		Remaining: remaining,
		ResetMs:   0,
	}, nil
}
