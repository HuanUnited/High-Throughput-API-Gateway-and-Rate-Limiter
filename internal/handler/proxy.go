package handler

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// ProxyConfig holds the configuration for the reverse proxy handler.
type ProxyConfig struct {
	// Target is the upstream backend URL to proxy requests to.
	Target *url.URL

	// Timeout is how long the proxy will wait for the upstream to respond.
	Timeout time.Duration
}

// NewProxyHandler creates a reverse proxy handler to the configured upstream.
// It injects X-Forwarded-* and X-Gateway headers onto each request.
func NewProxyHandler(cfg ProxyConfig) http.Handler {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite the request URL to target the upstream.
			pr.SetURL(cfg.Target)

			// Set X-Forwarded-* headers.
			pr.SetXForwarded()

			// Override Host header to match the upstream target.
			pr.Out.Host = cfg.Target.Host

			// Inject custom gateway header.
			pr.Out.Header.Set("X-Gateway", "go-limiter")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeJSONError(w, http.StatusBadGateway, "bad gateway")
		},
	}

	// Wrap with timeout for safety.
	return http.TimeoutHandler(proxy, cfg.Timeout, `{"error":"upstream timeout"}`)
}

// InjectGatewayHeaders is a standalone helper that adds gateway-specific headers.
// It's expressed as a http.HandlerFunc modifider for use in custom director chains.
func InjectGatewayHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ensure client IP is tracked for rate limiting keys.
		clientIP := r.Header.Get("X-Forwarded-For")
		if clientIP == "" {
			clientIP = r.RemoteAddr
		}
		r.Header.Set("X-Client-IP", clientIP)

		next.ServeHTTP(w, r)
	})
}

// parseTargetURL validates and parses an upstream URL string.
// Returns an error if the URL is invalid or unsupported.
func parseTargetURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errUnsupportedScheme
	}
	if u.Host == "" {
		return nil, errEmptyHost
	}
	return u, nil
}

// Error sentinel for unsupported URL schemes.
var errUnsupportedScheme = &proxyError{"unsupported url scheme; only http and https are supported"}

// Error sentinel for empty hosts.
var errEmptyHost = &proxyError{"url must include a host"}

// proxyError is a simple error type for proxy configuration errors.
type proxyError struct {
	message string
}

func (e *proxyError) Error() string {
	return e.message
}

// normalizePath ensures paths are prefixed with a slash for proper routing.
func normalizePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}
