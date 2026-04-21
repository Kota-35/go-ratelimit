package storage

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisStorage struct {
	client                     *redis.Client
	fixedWindowScript          *redis.Script
	slidingWindowCounterScript *redis.Script
	tokenBucketScript          *redis.Script
}

func NewRedisStorage(client *redis.Client) *RedisStorage {
	return &RedisStorage{
		client: client,
		// TODO: 各LuaScriptをprivateで作成
		fixedWindowScript:          redis.NewScript(luaFixedWindow),
		slidingWindowCounterScript: redis.NewScript(luaSlidingWindowCounter),
		tokenBucketScript:          redis.NewScript(luaTokenBucket),
	}
}

func (s *RedisStorage) Run(ctx context.Context, key string, args RunArgs) (LimiterResult, error) {
	var script *redis.Script
	var argv []interface{}

	switch a := args.(type) {
	case FixedWindowArgs:
		script = s.fixedWindowScript
		argv = []interface{}{a.Limit, int64(a.WindowSize.Seconds()), a.NowMs}
	case SlidingWindowCounterArgs:
		script = s.slidingWindowCounterScript
		argv = []interface{}{a.Limit, int64(a.WindowSize.Seconds()), a.NowMs}
	case TokenBucketArgs:
		script = s.tokenBucketScript
		argv = []interface{}{a.Capacity, a.RefillRate, a.NowMs}
	default:
		return LimiterResult{}, fmt.Errorf("unsupported args type: %T", args)
	}

	res, err := script.Run(ctx, s.client, []string{key}, argv...).Int64Slice()
	if err != nil {
		return LimiterResult{}, err
	}

	return LimiterResult{
		Allowed:   res[0] == 1,
		Remaining: int(res[1]),
		ResetMS:   res[2],
	}, nil
}
