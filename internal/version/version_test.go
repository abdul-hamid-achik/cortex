package version

import "testing"

func TestFull(t *testing.T) {
	Version, Commit, Date = "v1.2.3", "abc1234", "2026-07-06T00:00:00Z"
	got := Full()
	want := "v1.2.3 (commit abc1234, built 2026-07-06T00:00:00Z)"
	if got != want {
		t.Errorf("Full() = %q, want %q", got, want)
	}
}
