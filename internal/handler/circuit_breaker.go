package handler

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

// CircuitState circuit breaker state constants
type CircuitState int32

const (
	StateClosed CircuitState = iota
	StateHalfOpen
	StateOpen
)

// CircuitBreaker tracks failure rates and opens when threshold is exceeded.
type CircuitBreaker struct {
	mu              sync.Mutex
	state           CircuitState
	failures        int
	threshold       int
	cooldown        time.Duration
	lastStateChange time.Time
}

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:     StateClosed,
		threshold: threshold,
		cooldown:  cooldown,
	}
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	if cb.state == StateOpen {
		if now.Sub(cb.lastStateChange) > cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateChange = now
			return true
		}
		return false
	}
	if cb.state == StateHalfOpen {
		return false
	}
	return true
}

func (cb *CircuitBreaker) RecordResult(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if success {
		cb.failures = 0
		cb.state = StateClosed
		return
	}

	cb.failures++
	if cb.state == StateHalfOpen || cb.failures >= cb.threshold {
		cb.state = StateOpen
		cb.lastStateChange = time.Now()
	}
}

// resilientTransport adds retry with exponential backoff and circuit breaker protection.
type resilientTransport struct {
	base http.RoundTripper
	cb   *CircuitBreaker
}

func (rt *resilientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !rt.cb.Allow() {
		return nil, errors.New("circuit breaker is open")
	}

	isIdempotent := req.Method == http.MethodGet || req.Method == http.MethodHead || req.Method == http.MethodPut || req.Method == http.MethodDelete
	maxRetries := 1
	if isIdempotent {
		maxRetries = 3
	}

	var resp *http.Response
	var err error

	for attempt := range maxRetries {
		if attempt > 0 {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(time.Duration(attempt*50) * time.Millisecond):
			}

			if req.GetBody != nil {
				if body, bodyErr := req.GetBody(); bodyErr == nil {
					req.Body = body
				}
			}
		}

		resp, err = rt.base.RoundTrip(req)
		if err == nil && resp.StatusCode < 500 {
			rt.cb.RecordResult(true)
			return resp, nil
		}

		if attempt < maxRetries-1 && resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}

	rt.cb.RecordResult(false)
	return resp, err
}
