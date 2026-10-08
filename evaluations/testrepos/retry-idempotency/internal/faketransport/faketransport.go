// Package faketransport is an in-process http.RoundTripper that replays a
// script of canned responses, so retry behavior can be exercised with no
// network at all.
package faketransport

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// ErrConnection is what a scripted "err" step returns from RoundTrip.
var ErrConnection = errors.New("connection reset")

// Step is one scripted outcome.
type Step struct {
	Err        error
	Status     int
	RetryAfter string // value of the Retry-After header; empty for none
}

// Transport replays its steps in order. After the script is exhausted it keeps
// answering with the last step.
type Transport struct {
	mu    sync.Mutex
	steps []Step
	calls int
}

// New returns a Transport that replays steps.
func New(steps ...Step) *Transport {
	return &Transport{steps: steps}
}

// Calls reports how many requests have reached the transport.
func (t *Transport) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	i := t.calls
	t.calls++
	t.mu.Unlock()
	if len(t.steps) == 0 {
		return nil, errors.New("faketransport: empty script")
	}
	if i >= len(t.steps) {
		i = len(t.steps) - 1
	}
	s := t.steps[i]
	if s.Err != nil {
		return nil, s.Err
	}
	h := http.Header{}
	if s.RetryAfter != "" {
		h.Set("Retry-After", s.RetryAfter)
	}
	return &http.Response{
		StatusCode: s.Status,
		Status:     fmt.Sprintf("%d %s", s.Status, http.StatusText(s.Status)),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// Parse turns a comma-separated script such as "503/3,503,200" into steps.
// Each entry is a status code, optionally followed by "/" and the Retry-After
// header value, or the word "err" for a transport failure.
func Parse(script string) ([]Step, error) {
	var steps []Step
	for _, entry := range strings.Split(script, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "err" {
			steps = append(steps, Step{Err: ErrConnection})
			continue
		}
		code, after, _ := strings.Cut(entry, "/")
		status, err := strconv.Atoi(code)
		if err != nil || status < 100 || status > 599 {
			return nil, fmt.Errorf("bad script entry %q", entry)
		}
		steps = append(steps, Step{Status: status, RetryAfter: after})
	}
	return steps, nil
}
