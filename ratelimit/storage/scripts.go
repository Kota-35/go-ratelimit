package storage

import _ "embed"

//go:embed scripts/fixed_window.lua
var luaFixedWindow string

//go:embed scripts/sliding_window_counter.lua
var luaSlidingWindowCounter string

//go:embed scripts/token_bucket.lua
var luaTokenBucket string
