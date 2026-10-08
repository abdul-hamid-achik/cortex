package util

import (
	"testing"
	"time"
)

func TestOrDefault(t *testing.T) {
	if got := OrDefault(0, time.Second); got != time.Second {
		t.Fatalf("OrDefault(0) = %v, want 1s", got)
	}
	if got := OrDefault(2*time.Second, time.Second); got != 2*time.Second {
		t.Fatalf("OrDefault(2s) = %v, want 2s", got)
	}
}

func TestBackoffDoublesUpToLimit(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}
	for i, w := range want {
		if got := Backoff(i+1, time.Second, 5*time.Second); got != w {
			t.Fatalf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}
