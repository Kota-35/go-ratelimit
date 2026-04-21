-- スライディングウィンドウカウンター
--
-- KEYS[1]: ベースキー   (例: "user:alice")
-- ARGV[1]: limit      (integer, ウィンドウ内の最大リクエスト数)
-- ARGV[2]: window_sec (integer, ウィンドウ幅を秒で指定)
-- ARGV[3]: now_ms     (integer, Unixミリ秒)
--
-- 戻り値: {allowed(0|1), remaining(int), reset_ms(int)}

local key = KEYS[1]
local limit = assert(tonumber(ARGV[1]))
local window_sec = assert(tonumber(ARGV[2]))
local now_ms = assert(tonumber(ARGV[3]))

-- 秒単位の現在時刻(切り捨て)
local now_sec = math.floor(now_ms / 1000)

-- 現在・前のウィンドウ開始のタイムスタンプ(秒)
local curr_window_start = math.floor(now_sec / window_sec) * window_sec
local prev_window_start = curr_window_start - window_sec

local curr_key = key .. ":" .. curr_window_start
local prev_key = key .. ":" .. prev_window_start

local prev_count = tonumber(redis.call('GET', prev_key)) or 0
local curr_count = tonumber(redis.call('GET', curr_key)) or 0

-- 現在のウィンドウ内の経過秒 -> 前ウィンドウとの重なり率
local elapsed_sec = now_sec - curr_window_start
local overlap_rate = 1.0 - (elapsed_sec / window_sec)

local estimated = math.floor(prev_count * overlap_rate + curr_count)

-- 現在のウィンドウ終端までのm秒
local reset_ms = (curr_window_start + window_sec) * 1000 - now_ms

if estimated >= limit then
    return { 0, 0, reset_ms }
end

-- 現在のウィンドウをインクリメント & TTL の2ウィンドウ分保持
redis.call('INCR', curr_key)
redis.call('EXPIRE', curr_key, window_sec * 2)

local remaining = limit - estimated - 1
return { 1, remaining, 0 }
