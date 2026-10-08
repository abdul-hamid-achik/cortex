// Package util holds small helpers shared by several packages.
package util

import "time"

// OrDefault returns d unless it is zero or negative, in which case it returns
// fallback.
func OrDefault(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// Backoff returns the delay before retry attempt n (starting at 1), doubling
// from base and never exceeding limit.
func Backoff(n int, base, limit time.Duration) time.Duration {
	d := base
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	if d > limit {
		return limit
	}
	return d
}
