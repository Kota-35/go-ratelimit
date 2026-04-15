package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
)

// NewMiddleware は Store を注入した HTTP ミドルウェアを返す。
// リクエストヘッダー X-User-ID でユーザーを識別し、
// 許可時は X-RateLimit-Remaining / X-RateLimit-Reset を付与、
// 拒否時は 429 + Retry-After を返す。
func NewMiddleware(store Store, cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get("X-User-ID")
			if userID == "" {
				userID = "anonymous"
			}
			key := fmt.Sprintf("user:%s:rl", userID)

			result, err := store.Allow(r.Context(), key, cfg)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetMs, 10))

			if !result.Allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(result.ResetMs/1000+1, 10))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
