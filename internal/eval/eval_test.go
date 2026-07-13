package eval

import (
	"testing"
)

// TestEvalHarness runs the benchmark. A scenario passes only with a
// correct outcome AND an adequate evidence trail; scenarios needing a live
// backend self-skip. The scorecard is always printed for visibility.
func TestEvalHarness(t *testing.T) {
	scores := RunAll(t, Scenarios())

	ran, passed, skipped := 0, 0, 0
	t.Log("Cortex evaluation scorecard:")
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

// TestEvalHarnessPaired prints the deterministic baseline-vs-Cortex scorecard.
// The fixtures calibrate the measurement model; real recorded trials can fill
// the same PairedCase shape without changing the scorer.
func TestEvalHarnessPaired(t *testing.T) {
	summary, err := RunPaired(PairedFixtures())
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Paired evaluation scorecard (unassisted baseline → Cortex):")
	for _, c := range summary.Cases {
		t.Logf("  %-32s quality %.1f → %.1f (%+.1f), overall %.1f → %.1f (%+.1f)",
			c.Name, c.BaselineQuality, c.CortexQuality, c.QualityDelta,
			c.BaselineOverall, c.CortexOverall, c.OverallDelta)
		t.Logf("      baseline: %s", c.BaselineProtocol)
		for _, d := range c.Dimensions {
			t.Logf("      %-24s %.1f → %.1f (%+.1f)", d.Dimension, d.Baseline, d.Cortex, d.Delta)
		}
		t.Logf("      overhead: %+d calls, %+dms, %+dµ estimated cost",
			c.Overhead.ToolCalls, c.Overhead.LatencyMs, c.Overhead.EstimatedCostMicros)
	}
	t.Logf("paired mean: quality %.1f → %.1f (%+.1f), overall %.1f → %.1f (%+.1f), improved %d/%d",
		summary.MeanBaselineQuality, summary.MeanCortexQuality, summary.MeanQualityDelta,
		summary.MeanBaselineOverall, summary.MeanCortexOverall, summary.MeanOverallDelta,
		summary.CasesImproved, len(summary.Cases))

	if len(summary.Cases) == 0 {
		t.Fatal("paired evaluation has no cases")
	}
	if summary.MeanQualityDelta <= 0 || summary.MeanOverallDelta <= 0 {
		t.Errorf("paired fixtures should show positive Cortex lift: quality=%+.1f overall=%+.1f",
			summary.MeanQualityDelta, summary.MeanOverallDelta)
	}
	if summary.CasesRegressed != 0 {
		t.Errorf("%d paired fixture(s) regressed", summary.CasesRegressed)
	}
}
