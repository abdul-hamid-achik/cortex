package client

import (
	"strings"
	"testing"
	"time"

	"example.com/fetchd/internal/config"
)

func load(t *testing.T, text string) config.Config {
	t.Helper()
	cfg, err := config.Load(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTimeoutDefaultsWhenOmitted(t *testing.T) {
	c := New(load(t, "addr = db.internal:5432\n"))
	if got := c.Timeout(); got != 30*time.Second {
		t.Fatalf("Timeout() = %v, want 30s", got)
	}
}

func TestTimeoutFromConfig(t *testing.T) {
	c := New(load(t, "timeout = 5s\n"))
	if got := c.Timeout(); got != 5*time.Second {
		t.Fatalf("Timeout() = %v, want 5s", got)
	}
}

func TestAttempts(t *testing.T) {
	if got := New(load(t, "retries = 2\n")).Attempts(); got != 3 {
		t.Fatalf("Attempts() = %d, want 3", got)
	}
}
