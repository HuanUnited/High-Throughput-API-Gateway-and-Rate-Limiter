// Package handler provides HTTP handlers and reverse proxy logic.
package handler

import (
	"net/http"
	"net/http/httputil"
	"net/url"
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
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSONError(w, http.StatusBadGateway, "bad gateway")
		},
	}

	return http.TimeoutHandler(proxy, cfg.Timeout, `{"error":"upstream timeout"}`)
}
