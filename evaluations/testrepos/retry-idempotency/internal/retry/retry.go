package retry

import (
	"net/http"
	"time"
)

// Result describes how a Do call ended.
type Result struct {
	Response *http.Response
	Err      error
	// Attempts is how many requests were actually sent.
	Attempts int
	// Waited is the total delay requested between attempts.
	Waited time.Duration
}

// Do sends req through rt, retrying according to p. Between attempts it calls
// sleep with the delay to wait, so callers and tests control real time.
func Do(rt http.RoundTripper, req *http.Request, p Policy, sleep func(time.Duration)) Result {
	var res Result
	for attempt := 1; ; attempt++ {
		resp, err := rt.RoundTrip(req)
		res.Response, res.Err, res.Attempts = resp, err, attempt
		if attempt >= p.MaxAttempts || !p.Retryable(req, resp, err) {
			return res
		}
		d := delay(p, attempt, resp)
		res.Waited += d
		sleep(d)
	}
}

// delay is how long to wait after the attempt-th failed attempt.
func delay(p Policy, attempt int, resp *http.Response) time.Duration {
	return p.Backoff(attempt)
}
