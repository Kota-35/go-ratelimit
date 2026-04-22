package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go-ratelimit/ratelimit"
	"go-ratelimit/ratelimit/limiter"
	"go-ratelimit/ratelimit/storage"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func main() {
	algorithm := os.Getenv("ALGORITHM")
	if algorithm == "" {
		algorithm = "token_bucket"
	}

	var store storage.Storage
	var storageKind string
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
		store = storage.NewRedisStorage(rdb)
		storageKind = "redis"
		log.Printf("using RedisStorage: %s", redisAddr)
	} else {
		storageKind = "memory"
		store = storage.NewMemoryStorage()
		log.Println("using MemoryStorage")
	}

	var l limiter.Limiter
	switch algorithm {
	case "fixed_window":
		cfg := limiter.FixedWindowConfig{
			Limit:      10,
			WindowSize: 60 * time.Second,
		}
		l = limiter.NewFixedWindowLimiter(store, cfg)
		log.Printf("algorithm: fixed_window limit=%d window=%s", cfg.Limit, cfg.WindowSize)
	case "sliding_window_counter":
		cfg := limiter.SlidingWindowCounterConfig{
			Limit:      10,
			WindowSize: 60 * time.Second,
		}
		l = limiter.NewSlidingWindowCounterConfig(store, cfg)
		log.Printf("algorithm: sliding_window_counter limit=%d window=%s", cfg.Limit, cfg.WindowSize)
	default:
		cfg := limiter.TokenBucketConfig{
			Capacity:   10,
			RefillRate: 1,
		}
		l = limiter.NewTokenBucketLimiter(store, cfg)
		log.Printf("algorithm: token_bucket capacity=%.0f refill_rate=%.2f/s", cfg.Capacity, cfg.RefillRate)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		name := os.Getenv("SERVER_NAME")
		w.Header().Set("X-Server-ID", name)
		fmt.Fprintf(w, "pong from %s\n", name)
	})
	mux.Handle("/metrics", promhttp.Handler())

	handler := ratelimit.NewMiddleware(l, algorithm, storageKind)(mux)

	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
