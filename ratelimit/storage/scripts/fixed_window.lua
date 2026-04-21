-- INCR → EXPIRE → 判定 を atomic に実行
--
-- KEYS[1]  : カウンターキー (例: "user:alice:rl")
-- ARGV[1]  : limit      (integer, ウィンドウ内の最大リクエスト数)
-- ARGV[2]  : window_sec (integer, ウィンドウ幅を秒単位で指定)
--
-- 戻り値: {allowed(0|1), remaining(int), ttl_ms(int)}
local count = redis.call('INCR', KEYS[1])
local limit = assert(tonumber(ARGV[1]))

-- count == 1 のとき = このウィンドウの最初のリクエスト
-- 初回のみ EXPIRE をセットすることでウィンドウの開始時刻を確定する
if count == 1 then
    redis.call('EXPIRE', KEYS[1], assert(tonumber(ARGV[2])))
end

-- PTTL: キーの残り有効期限をミリ秒で取得する
-- reset_ms としてクライアントへ返すことで、いつウィンドウがリセットされるかを伝える
local ttl_ms = redis.call('PTTL', KEYS[1])

local allowed = 0
local remaining = 0

if count <= limit then
    allowed = 1
    remaining = limit - count
end

return { allowed, remaining, ttl_ms }
