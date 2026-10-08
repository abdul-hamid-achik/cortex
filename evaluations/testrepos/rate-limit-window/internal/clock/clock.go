// Package clock supplies time sources and the window arithmetic shared by the
// rate limiters. Timestamps are whole milliseconds so tests and tools can pass
// them around as plain integers.
package clock

// Millis is a point in time, in milliseconds since an arbitrary epoch.
type Millis int64

// Clock reports the current time.
type Clock interface {
	Now() Millis
}

// Manual is a Clock that only moves when told to.
type Manual struct {
	t Millis
}

// NewManual returns a Manual clock reading start.
func NewManual(start Millis) *Manual { return &Manual{t: start} }

// Now returns the current reading.
func (m *Manual) Now() Millis { return m.t }

// Advance moves the clock forward by d milliseconds.
func (m *Manual) Advance(d Millis) { m.t += d }

// Set jumps the clock to t.
func (m *Manual) Set(t Millis) { m.t = t }

// ExpiresAt returns the first instant at which an event stamped ts stops
// counting against a window of the given length.
func ExpiresAt(ts, window Millis) Millis {
	return ts + window
}

// Expired reports whether an event stamped ts has aged out of a window of the
// given length when observed at now.
func Expired(ts, now, window Millis) bool {
	return now-ts > window
}
