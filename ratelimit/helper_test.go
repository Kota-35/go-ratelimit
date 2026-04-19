package ratelimit_test

import "go-ratelimit/ratelimit"

var testCfg = ratelimit.Config{Capacity: 5, RefillRate: 1}
