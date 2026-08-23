package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProxyHandler(t *testing.T) {
	t.Run("proxies request and injects headers", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "go-limiter", r.Header.Get("X-Gateway"))
			assert.NotEmpty(t, r.Header.Get("X-Forwarded-For"))
			w.Header().Set("X-Upstream-Header", "present")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"proxied"}`))
		}))
		defer upstream.Close()

		targetURL, err := url.Parse(upstream.URL)
		require.NoError(t, err)

		proxy := NewProxyHandler(ProxyConfig{
			Target:  targetURL,
			Timeout: 2 * time.Second,
		})

		req := httptest.NewRequest(http.MethodGet, "/test/path", nil)
		rec := httptest.NewRecorder()

		proxy.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "go-limiter", rec.Header().Get("X-Gateway"))
		assert.Equal(t, "present", rec.Header().Get("X-Upstream-Header"))
	})

	t.Run("returns 502 on unreachable upstream", func(t *testing.T) {
		deadURL, _ := url.Parse("http://127.0.0.1:54321")
		proxy := NewProxyHandler(ProxyConfig{
			Target:  deadURL,
			Timeout: 1 * time.Second,
		})

		req := httptest.NewRequest(http.MethodGet, "/unreachable", nil)
		rec := httptest.NewRecorder()

		proxy.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")

		var pd ProblemDetails
		err := json.NewDecoder(rec.Body).Decode(&pd)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadGateway, pd.Status)
		assert.Equal(t, "Bad Gateway", pd.Title)
	})

	t.Run("returns 504 on upstream timeout", func(t *testing.T) {
		slowUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer slowUpstream.Close()

		targetURL, _ := url.Parse(slowUpstream.URL)
		proxy := NewProxyHandler(ProxyConfig{
			Target:  targetURL,
			Timeout: 20 * time.Millisecond,
		})

		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		rec := httptest.NewRecorder()

		proxy.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
	})
}
