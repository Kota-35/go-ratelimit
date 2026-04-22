package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go-ratelimit/ratelimit/limiter"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limit_requests_total",
		Help: "レート制限を通過したリクエストの総数. result ラベルが allowed/denied を示す",
	}, []string{"algorithm", "storage", "result"})

	remainingGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rate_limit_remaining",
		Help: "直近リクエスト後の残許可トークン/カウント数",
	}, []string{"algorithm"})

	decisionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "rate_limit_decision_duration_seconds",
		Help:    "レート制限判断にかかった時間",
		Buckets: prometheus.DefBuckets,
	})

	storageErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limit_storage_errors_total",
		Help: "ストレージ操作のエラー総数",
	}, []string{"kind"})
)

// NewMiddleware は Limiter を注入した HTTP ミドルウェアを返す。
// algorithm にはアルゴリズム名（Prometheus ラベル用）を渡す。
// リクエストヘッダー X-User-ID でユーザーを識別し、
// 許可時は X-RateLimit-Remaining / X-RateLimit-Reset を付与、
// 拒否時は 429 + Retry-After を返す。
func NewMiddleware(l limiter.Limiter, algorithm string, storage string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get("X-User-ID")
			if userID == "" {
				userID = "anonymous"
			}
			key := fmt.Sprintf("user:%s:rl", userID)

			start := time.Now()
			result, err := l.Allow(r.Context(), key)
			decisionDuration.Observe(float64(time.Since(start).Seconds()))
			if err != nil {
				storageErrors.WithLabelValues(storage).Inc()
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetMs, 10))

			remainingGauge.WithLabelValues(algorithm).Set(float64(result.Remaining))

			if !result.Allowed {
				requestsTotal.WithLabelValues(algorithm, storage, "denied").Inc()
				// NOTE: Retry-After は RFC 7231 で整数秒と定義されている
				// https://datatracker.ietf.org/doc/html/rfc7231
				retryAfter := (result.ResetMs + 999) / 1000
				w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}

			requestsTotal.WithLabelValues(algorithm, storage, "allowed").Inc()
			next.ServeHTTP(w, r)
		})
	}
}
