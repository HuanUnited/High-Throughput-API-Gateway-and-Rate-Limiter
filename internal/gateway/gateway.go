// Package gateway implements the reverse-proxy routing and upstream management.
// Skeleton: proxy, health checks, and circuit breaker added in later commits.
package gateway

import "net/http"

// Gateway is the reverse proxy fronting upstream services.
type Gateway struct {
	upstreamURL string
}

// New creates a Gateway that proxies requests to the given upstream URL.
func New(upstreamURL string) *Gateway {
	return &Gateway{
		upstreamURL: upstreamURL,
	}
}

// Handler returns the HTTP handler that proxies requests to the upstream.
// Skeleton: real proxy implementation added in later commits.
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway proxy not yet implemented", http.StatusNotImplemented)
	})
}
