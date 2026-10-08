// Package limit implements request rate limiters.
package limit

import "example.com/ratelimit/internal/clock"

// Limiter admits at most limit requests in any sliding window of the given
// length. It keeps the timestamps of admitted requests, oldest first.
type Limiter struct {
	limit  int
	window clock.Millis
	events []clock.Millis
}

// New returns a Limiter allowing limit requests per window milliseconds.
func New(limit int, window clock.Millis) *Limiter {
	return &Limiter{limit: limit, window: window}
}

// Allow records a request made at now and reports whether it is admitted.
// Rejected requests are not recorded.
func (l *Limiter) Allow(now clock.Millis) bool {
	l.prune(now)
	if len(l.events) >= l.limit {
		return false
	}
	l.events = append(l.events, now)
	return true
}

// Remaining reports how many more requests would be admitted at now, without
// recording anything.
func (l *Limiter) Remaining(now clock.Millis) int {
	live := 0
	for _, ts := range l.events {
		if now-ts <= l.window {
			live++
		}
	}
	if live >= l.limit {
		return 0
	}
	return l.limit - live
}

// RetryAfter reports how many milliseconds a caller rejected at now must wait
// before a request would be admitted. It is zero when a request is admissible.
func (l *Limiter) RetryAfter(now clock.Millis) clock.Millis {
	live := l.liveSince(now)
	if len(live) < l.limit {
		return 0
	}
	wait := clock.ExpiresAt(live[0], l.window) - now
	if wait < 0 {
		return 0
	}
	return wait
}

func (l *Limiter) prune(now clock.Millis) {
	l.events = l.liveSince(now)
}

func (l *Limiter) liveSince(now clock.Millis) []clock.Millis {
	i := 0
	for i < len(l.events) && clock.Expired(l.events[i], now, l.window) {
		i++
	}
	return l.events[i:]
}
