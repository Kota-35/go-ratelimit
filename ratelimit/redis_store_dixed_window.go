package ratelimit

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// fixedWindowLua はカウントインクリメント・TTL設定・判定を atomic に実行する Lua スクリプト。
//
// KEYS[1] : カウンターキー
// ARGV[1] : ウィンドウ内の最大リクエスト数 (int)
// ARGV[2] : ウィンドウのサイズ (秒, int)
//
// 戻り値: {allowed(0|1), remaining(int), reset_ms(int)}
const fixedWindowLua = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
    redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
end

local limit = tonumber(ARGV[1])
local ttl_ms = redis.call('PTTL', KEYS[1])

local allowed = 0
local remaining = 0

if count <= limit then
    allowed = 1
    remaining = limit - count
end

return {allowed, remaining, ttl_ms}
`

type RedisStoreFixedWindow struct {
	client *redis.Client
	script *redis.Script
}

func NewRedisStoreFixedWindow(client *redis.Client) *RedisStoreFixedWindow {
	return &RedisStoreFixedWindow{
		client: client,
		script: redis.NewScript(fixedWindowLua),
	}
}

func (s *RedisStoreFixedWindow) Allow(ctx context.Context, key string, cfg Config) (Result, error) {
	c, ok := cfg.(FixedWindowConfig)
	if !ok {
		return Result{}, fmt.Errorf("RedisStoreFixedWindow: got %T, want FixedWindowConfig", cfg)
	}

	res, err := s.script.Run(ctx, s.client,
		[]string{key},
		c.Limit, c.WindowSecs,
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
