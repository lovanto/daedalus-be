package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/daedalus/daedalus-be/utils"
)

const (
	rateLimitMax  = 100            // max tokens per window
	rateFillRate  = 100.0 / 60.0  // tokens per second (100 per minute)
)

type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// bucketStore holds one bucket per rate-limit key.
var bucketStore sync.Map // key string → *tokenBucket

// RateLimit enforces 100 requests/minute per authenticated user (falls back to
// remote IP for unauthenticated requests).
func RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := GetUserID(r)
		if key == "" {
			key = r.RemoteAddr
		}

		raw, _ := bucketStore.LoadOrStore(key, &tokenBucket{
			tokens: rateLimitMax,
			last:   time.Now(),
		})
		b := raw.(*tokenBucket)

		b.mu.Lock()
		now := time.Now()
		b.tokens += now.Sub(b.last).Seconds() * rateFillRate
		if b.tokens > rateLimitMax {
			b.tokens = rateLimitMax
		}
		b.last = now

		ok := b.tokens >= 1
		if ok {
			b.tokens--
		}
		b.mu.Unlock()

		if !ok {
			w.Header().Set("Retry-After", "60")
			utils.Err(w, http.StatusTooManyRequests, "rate limit exceeded — 100 req/min per user")
			return
		}

		next.ServeHTTP(w, r)
	})
}
