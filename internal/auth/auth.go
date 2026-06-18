// Package auth handles client authentication and identity resolution.
// Skeleton: API key, JWT, and identity extraction added in later commits.
package auth

import (
	"context"
	"net/http"
)

// Identity represents an authenticated client.
type Identity struct {
	ID   string
	Kind string // e.g., "apikey", "jwt", "anonymous"
}

// Authenticator verifies credentials and returns the client identity.
type Authenticator interface {
	// Authenticate extracts and verifies the client's identity
	// from the incoming request.
	Authenticate(ctx context.Context, r *http.Request) (*Identity, error)
}
