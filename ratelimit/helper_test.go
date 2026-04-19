package ratelimit_test

import "go-ratelimit/ratelimit"

var testCfg = ratelimit.TokenBucketConfig{Capacity: 5, RefillRate: 1}
