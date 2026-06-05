package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// statusWriter wraps ResponseWriter to capture the written status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(status int) {
	sw.status = status
	sw.ResponseWriter.WriteHeader(status)
}

// StructuredLogger logs every request as a structured slog record.
// Use this instead of chi's default Logger in production mode.
func StructuredLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)

		slog.Info("http",
			"method",      r.Method,
			"path",        r.URL.Path,
			"status",      sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"user_id",     GetUserID(r),
			"request_id",  GetRequestID(r),
			"remote_addr", r.RemoteAddr,
		)
	})
}
