package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const requestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// RequestID injects a unique request ID into every request context and response header.
// If the caller already supplies X-Request-ID, that value is forwarded.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			b := make([]byte, 8)
			rand.Read(b) //nolint:errcheck
			id = hex.EncodeToString(b)
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID returns the request ID stored in the context by RequestID middleware.
func GetRequestID(r *http.Request) string {
	v, _ := r.Context().Value(requestIDKey{}).(string)
	return v
}
