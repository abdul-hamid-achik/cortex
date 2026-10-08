package limit

import "example.com/ratelimit/internal/clock"

// Bucket is a token bucket: it holds up to capacity tokens and refills at
// perSecond tokens per second. It is a separate, self-contained policy from
// the sliding-window Limiter and does not share its window arithmetic.
type Bucket struct {
	capacity  float64
	perSecond float64
	tokens    float64
	last      clock.Millis
}

// NewBucket returns a full bucket whose clock starts at start.
func NewBucket(capacity, perSecond float64, start clock.Millis) *Bucket {
	return &Bucket{capacity: capacity, perSecond: perSecond, tokens: capacity, last: start}
}

// Take removes one token at now and reports whether one was available.
func (b *Bucket) Take(now clock.Millis) bool {
	if now > b.last {
		b.tokens += float64(now-b.last) / 1000 * b.perSecond
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
