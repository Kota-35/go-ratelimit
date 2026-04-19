package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"go-ratelimit/ratelimit"

	"github.com/redis/go-redis/v9"
)

func main() {
	algorithm := os.Getenv("ALGORITHM")

	cfg := ratelimit.TokenBucketConfig{
		Capacity:   10,
		RefillRate: 1,
	}
	log.Printf("config: capacity=%.0f refill_rate=%.2f/s", cfg.Capacity, cfg.RefillRate)

	var store ratelimit.Store
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
		switch algorithm {
		case "token_bucket":
			store = ratelimit.NewRedisStoreTokenBucket(rdb)
		default:
			store = ratelimit.NewRedisStoreTokenBucket(rdb)
		}
		log.Printf("using RedisStore: %s", redisAddr)
	} else {
		store = ratelimit.NewMemoryStoreMutex()
		log.Println("using MemoryStore")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		name := os.Getenv("SERVER_NAME")
		w.Header().Set("X-Server-ID", name)
		fmt.Fprintf(w, "pong from %s\n", name)
	})

	handler := ratelimit.NewMiddleware(store, cfg)(mux)

	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
