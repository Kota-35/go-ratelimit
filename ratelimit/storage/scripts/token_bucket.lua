-- luaScript は GET → 計算 → SET を atomic に実行する Lua スクリプト。
--
-- Why: Go側で HGET -> 計算 -> SET の3ステップに分けて書かずに Lua スクリプトにしているのか？
-- Luaスクリプトにすると Redisがシングルスレッドで全行を中断なく実行するから
-- ```
-- 時刻T1: goroutine A が HGET -> tokens=1 を読む
-- 時刻T2: goroutine B が HGET -> tokens=1 を読む // Aがまだ書いていない
-- 時刻T3: A が計算 -> tokens-1=0 -> SET
-- 時刻T4: B が計算 -> tokens-1=0 -> SET // 本来は拒否すべきなのに通過してしまう。
-- ```
-- これがTOCTOU(Time of Check to Time of Use)競合
-- ref: https://ja.wikipedia.org/wiki/Time-of-check_to_time-of-use
--
-- KEYS[1]  : ハッシュキー (例: "user:alice:rl")
-- ARGV[1]  : capacity  (float)
-- ARGV[2]  : refill_rate (tokens/sec, float)
-- ARGV[3]  : 現在時刻 (Unix ミリ秒, integer)
--
-- 戻り値: {allowed(0|1), remaining(int), reset_ms(int)}
local data = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill')
local capacity   = assert(tonumber(ARGV[1]))
local refill_rate = assert(tonumber(ARGV[2]))
local now        = assert(tonumber(ARGV[3]))

-- data[1]がnil = Redisにキーが存在しない = 初回リクエスト
-- tokens を capacity で一杯にするのは新規ユーザーは制限なしでスタートできるから
local tokens     = tonumber(data[1]) or capacity
local last_refill = tonumber(data[2]) or now

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
return { allowed, math.floor(tokens), reset_ms }
