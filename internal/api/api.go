// Package api defines internal API contracts: typed errors and shared models.
// These are shared between handlers, services, and storage to avoid cycles.
package api

import "net/http"

// ErrorKind categorizes API errors for consistent client responses.
type ErrorKind string

const (
	ErrorKindBadRequest   ErrorKind = "bad_request"
	ErrorKindUnauthorized ErrorKind = "unauthorized"
	ErrorKindForbidden    ErrorKind = "forbidden"
	ErrorKindRateLimited  ErrorKind = "rate_limited"
	ErrorKindNotFound     ErrorKind = "not_found"
	ErrorKindInternal     ErrorKind = "internal"
)

// Error is a typed API error with an associated HTTP status code.
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
	Status  int       `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Message
}

// NewError creates a new typed API error.
func NewError(kind ErrorKind, message string) *Error {
	return &Error{
		Kind:    kind,
		Message: message,
		Status:  statusForKind(kind),
	}
}

// statusForKind maps an error kind to an HTTP status code.
func statusForKind(kind ErrorKind) int {
	switch kind {
	case ErrorKindBadRequest:
		return http.StatusBadRequest
	case ErrorKindUnauthorized:
		return http.StatusUnauthorized
	case ErrorKindForbidden:
		return http.StatusForbidden
	case ErrorKindRateLimited:
		return http.StatusTooManyRequests
	case ErrorKindNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}
