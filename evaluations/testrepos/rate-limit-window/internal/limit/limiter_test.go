package limit

import (
	"testing"

	"example.com/ratelimit/internal/clock"
)

func TestAllowUpToLimit(t *testing.T) {
	l := New(3, 1000)
	for i, ts := range []clock.Millis{0, 100, 200} {
		if !l.Allow(ts) {
			t.Fatalf("request %d at %d rejected", i, ts)
		}
	}
	if l.Allow(500) {
		t.Fatalf("fourth request inside the window was admitted")
	}
}

func TestWindowSlides(t *testing.T) {
	l := New(2, 1000)
	l.Allow(0)
	l.Allow(300)
	if l.Allow(900) {
		t.Fatalf("request at 900 admitted while two are live")
	}
	if !l.Allow(1200) {
		t.Fatalf("request at 1200 rejected after the first aged out")
	}
	if l.Allow(1250) {
		t.Fatalf("request at 1250 admitted while two are live")
	}
}

func TestRemaining(t *testing.T) {
	l := New(3, 1000)
	l.Allow(0)
	l.Allow(200)
	if got := l.Remaining(300); got != 1 {
		t.Fatalf("Remaining(300) = %d, want 1", got)
	}
	if got := l.Remaining(1100); got != 2 {
		t.Fatalf("Remaining(1100) = %d, want 2", got)
	}
	l.Allow(300)
	if got := l.Remaining(400); got != 0 {
		t.Fatalf("Remaining(400) = %d, want 0", got)
	}
}

func TestRetryAfter(t *testing.T) {
	l := New(1, 1000)
	l.Allow(100)
	if got := l.RetryAfter(400); got != 700 {
		t.Fatalf("RetryAfter(400) = %d, want 700", got)
	}
	if got := l.RetryAfter(2000); got != 0 {
		t.Fatalf("RetryAfter(2000) = %d, want 0", got)
	}
}
