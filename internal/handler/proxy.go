package handler

import (
	"context"
	"errors"
	"net"
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
	// Transport specifies the round tripper for connection pooling
	Transport http.RoundTripper
}

// NewProxyHandler creates a reverse proxy handler to the configured upstream.
func NewProxyHandler(cfg ProxyConfig) http.Handler {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	cb := NewCircuitBreaker(5, 5*time.Second)

	transport := cfg.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        20000,
			MaxIdleConnsPerHost: 20000,
			MaxConnsPerHost:     20000,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		}
	}

	proxyTransport := &resilientTransport{
		base: transport,
		cb:   cb,
	}

	proxy := &httputil.ReverseProxy{
		Transport: proxyTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.Target)
			pr.SetXForwarded()
			pr.Out.Host = cfg.Target.Host
			pr.Out.Header.Set("X-Gateway", "go-limiter")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Gateway", "go-limiter")
			if xfHost := resp.Request.Header.Get("X-Forwarded-Host"); xfHost != "" {
				resp.Header.Set("X-Forwarded-Host", xfHost)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if strings.Contains(err.Error(), "circuit breaker is open") {
				writeProblemDetails(w, http.StatusServiceUnavailable, ProblemDetails{
					Type:     "https://tools.ietf.org/html/rfc7231#section-6.6.4",
					Title:    "Service Unavailable",
					Status:   http.StatusServiceUnavailable,
					Detail:   "Upstream circuit breaker is open. Please try again later.",
					Instance: r.URL.Path,
				})
				return
			}

			if errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded) {
				writeProblemDetails(w, http.StatusGatewayTimeout, ProblemDetails{
					Type:     "https://tools.ietf.org/html/rfc7231#section-6.6.5",
					Title:    "Gateway Timeout",
					Status:   http.StatusGatewayTimeout,
					Detail:   "Upstream connection timed out.",
					Instance: r.URL.Path,
				})
				return
			}

			writeProblemDetails(w, http.StatusBadGateway, ProblemDetails{
				Type:     "https://tools.ietf.org/html/rfc7231#section-6.6.3",
				Title:    "Bad Gateway",
				Status:   http.StatusBadGateway,
				Detail:   "The upstream service failed to respond or returned an invalid response.",
				Instance: r.URL.Path,
			})
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeout)
		defer cancel()
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}
