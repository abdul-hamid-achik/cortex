// Package client describes how fetchd talks to its upstream.
package client

import (
	"time"

	"example.com/fetchd/internal/config"
	"example.com/fetchd/internal/util"
)

// Client carries the settings one upstream request is made with. A zero
// timeout means "wait forever" to the HTTP layer, so it must never be zero.
type Client struct {
	addr    string
	timeout time.Duration
	retries int
}

// New builds a Client from loaded configuration.
func New(cfg config.Config) *Client {
	return &Client{addr: cfg.Addr, timeout: cfg.Timeout, retries: cfg.Retries}
}

// Addr is the upstream address requests are sent to.
func (c *Client) Addr() string { return c.addr }

// Timeout is how long a single request may take.
func (c *Client) Timeout() time.Duration { return c.timeout }

// RetryDelay is the pause before retry attempt n (starting at 1).
func (c *Client) RetryDelay(n int) time.Duration {
	return util.Backoff(n, 100*time.Millisecond, c.timeout/2)
}

// Attempts is the total number of tries, including the first.
func (c *Client) Attempts() int { return c.retries + 1 }
