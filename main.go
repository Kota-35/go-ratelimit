package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"go-ratelimit/ratelimit"

	"github.com/redis/go-redis/v9"
)

func envFloat(key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func main() {
	cfg := ratelimit.Config{
		Capacity:   envFloat("CAPACITY", 10),
		RefillRate: envFloat("REFILL_RATE", 1),
	}
	log.Printf("config: capacity=%.0f refill_rate=%.2f/s", cfg.Capacity, cfg.RefillRate)

	var store ratelimit.Store
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
		store = ratelimit.NewRedisStore(rdb)
		log.Printf("using RedisStore: %s", redisAddr)
	} else {
		store = ratelimit.NewMemoryStoreSyncMap()
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
