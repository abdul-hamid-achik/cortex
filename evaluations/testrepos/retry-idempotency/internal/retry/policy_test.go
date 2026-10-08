package retry

import (
	"net/http"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	p := Policy{MaxAttempts: 5, BaseDelay: 50 * time.Millisecond}
	for n, want := range map[int]time.Duration{
		1: 50 * time.Millisecond,
		2: 100 * time.Millisecond,
		3: 200 * time.Millisecond,
	} {
		if got := p.Backoff(n); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestRetryableStatuses(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	p := DefaultPolicy()
	for status, want := range map[int]bool{
		200: false, 301: false, 400: false, 404: false, 500: false,
		429: true, 502: true, 503: true, 504: true,
	} {
		if got := p.Retryable(req, &http.Response{StatusCode: status}, nil); got != want {
			t.Errorf("Retryable(%d) = %v, want %v", status, got, want)
		}
	}
}
