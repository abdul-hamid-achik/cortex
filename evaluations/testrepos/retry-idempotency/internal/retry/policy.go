// Package retry re-sends failed HTTP requests with exponential backoff.
package retry

import (
	"net/http"
	"time"
)

// Policy controls how many attempts are made and how long to back off.
type Policy struct {
	// MaxAttempts is the total number of attempts, including the first.
	MaxAttempts int
	// BaseDelay is the backoff before the second attempt; it doubles after
	// every further failure.
	BaseDelay time.Duration
}

// DefaultPolicy makes up to four attempts, backing off 100ms, 200ms, 400ms.
func DefaultPolicy() Policy {
	return Policy{MaxAttempts: 4, BaseDelay: 100 * time.Millisecond}
}

// Backoff returns the delay to wait after the n-th failed attempt (n >= 1).
func (p Policy) Backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return p.BaseDelay << (n - 1)
}

// Retryable reports whether an attempt that ended with resp or err should be
// tried again. Transport errors and the usual transient statuses are retried.
func (p Policy) Retryable(req *http.Request, resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}
