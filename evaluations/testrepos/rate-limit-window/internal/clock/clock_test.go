package clock

import "testing"

func TestManualClock(t *testing.T) {
	m := NewManual(100)
	if m.Now() != 100 {
		t.Fatalf("now = %d, want 100", m.Now())
	}
	m.Advance(50)
	if m.Now() != 150 {
		t.Fatalf("now = %d, want 150", m.Now())
	}
	m.Set(7)
	if m.Now() != 7 {
		t.Fatalf("now = %d, want 7", m.Now())
	}
}

func TestExpiresAt(t *testing.T) {
	if got := ExpiresAt(250, 1000); got != 1250 {
		t.Fatalf("ExpiresAt = %d, want 1250", got)
	}
}

func TestExpired(t *testing.T) {
	cases := []struct {
		name         string
		ts, now, win Millis
		want         bool
	}{
		{"fresh", 1000, 1200, 1000, false},
		{"long gone", 0, 5000, 1000, true},
		{"just after", 0, 1500, 1000, true},
		{"same instant", 400, 400, 1000, false},
	}
	for _, c := range cases {
		if got := Expired(c.ts, c.now, c.win); got != c.want {
			t.Errorf("%s: Expired(%d, %d, %d) = %v, want %v", c.name, c.ts, c.now, c.win, got, c.want)
		}
	}
}
