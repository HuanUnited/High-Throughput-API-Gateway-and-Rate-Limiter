// Package middleware provides reusable, framework-agnostic HTTP middleware.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDKey is the context key for the request ID.
type RequestIDKey struct{}

// RequestID generates and attaches a unique request ID to each request.
// It propagates an existing X-Request-ID header if present.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = newRandomID()
		}

		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), RequestIDKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newRandomID returns a cryptographically random 16-byte hex string.
func newRandomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to a non-crypto-random placeholder on catastrophic failure.
		return "unknown-request-id"
	}
	return hex.EncodeToString(buf)
}
