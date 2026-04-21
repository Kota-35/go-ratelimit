local count = redis.call('INCR', KEYS[1])
local limit = assert(tonumber(ARGV[1]))
if count == 1 then
    redis.call('EXPIRE', KEYS[1], assert(tonumber(ARGV[2])))
end
local ttl_ms = redis.call('PTTL', KEYS[1])

local allowed = 0
local remaining = 0

if count <= limit then
    allowed = 1
    remaining = limit - count
end

return { allowed, remaining, ttl_ms }
