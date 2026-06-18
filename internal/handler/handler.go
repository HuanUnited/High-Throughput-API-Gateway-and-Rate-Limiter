// Package handler contains the HTTP layer: routing, middleware wiring,
// and request/response handling. Business logic lives in other packages.
package handler

import (
	"encoding/json"
	"net/http"
)

// Handler is the HTTP handler container.
// It will hold dependencies (ratelimiter, gateway, auth) in later commits.
type Handler struct {
	// deps to be injected in later commits
}

// New creates a new Handler with its dependencies.
func New() *Handler {
	return &Handler{}
}

// Routes registers all HTTP routes on the given mux.
// Skeleton: extended in later commits as handlers are implemented.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.notFound())
}

// notFound returns a standardized 404 JSON response.
func (h *Handler) notFound() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "resource not found",
		})
	}
}

// writeJSON serializes the payload as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
