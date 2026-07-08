package eval

import (
	"testing"
)

// TestEvalHarness runs the §18.3 benchmark. A scenario passes only with a
// correct outcome AND an adequate evidence trail; scenarios needing a live
// backend self-skip. The scorecard is always printed for visibility.
func TestEvalHarness(t *testing.T) {
	scores := RunAll(t, Scenarios())

	ran, passed, skipped := 0, 0, 0
	t.Log("Cortex evaluation scorecard (SPEC §18.3):")
	for _, s := range scores {
		switch {
		case s.Skipped:
			skipped++
			t.Logf("  SKIP  %-32s (%s)", s.Name, s.Reason)
		case s.Passed():
			ran++
			passed++
			t.Logf("  PASS  %-32s [%s]", s.Name, s.Category)
		default:
			ran++
			t.Logf("  FAIL  %-32s [%s]", s.Name, s.Category)
			for _, f := range s.Findings {
				t.Logf("          - %s", f)
			}
		}
	}
	t.Logf("scorecard: %d/%d authored scenarios passed, %d skipped (pending or needs a live backend)", passed, ran, skipped)

	for _, s := range scores {
		if !s.Skipped && !s.Passed() {
			t.Errorf("scenario %q failed its outcome/evidence checks", s.Name)
		}
	}
}
