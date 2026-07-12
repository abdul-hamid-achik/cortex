package eval

import (
	"math"
	"strings"
	"testing"
)

func TestScorePairMeasuresQualityLiftAndOverhead(t *testing.T) {
	r, err := ScorePair(knownSymbolPair())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Dimensions) != len(EvaluationDimensions()) {
		t.Fatalf("dimensions = %d, want %d", len(r.Dimensions), len(EvaluationDimensions()))
	}
	if r.CortexQuality != 100 {
		t.Fatalf("cortex quality = %.2f, want 100", r.CortexQuality)
	}
	if r.QualityDelta <= 0 || r.OverallDelta <= 0 {
		t.Fatalf("expected positive lift, quality=%+.2f overall=%+.2f", r.QualityDelta, r.OverallDelta)
	}
	if r.Overhead.ToolCalls != 3 || r.Overhead.LatencyMs != 800 || r.Overhead.EstimatedCostMicros != -2_000 {
		t.Fatalf("unexpected raw overhead: %+v", r.Overhead)
	}
	cost := pairedDimension(t, r, DimensionCostLatencyOverhead)
	if cost.Delta >= 0 {
		t.Fatalf("cost guardrail should expose the added tool/latency overhead, got delta %.2f", cost.Delta)
	}
}

func TestDimensionScorersHaveStableEndpoints(t *testing.T) {
	tests := []struct {
		name string
		got  DimensionScore
		want float64
	}{
		{"evidence perfect", scoreEvidence(EvidenceObservation{Required: true, Items: 2, Sourced: 2, ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 1, Candidates: 1, CandidatesKeptAsCandidates: 1}), 100},
		{"evidence absent", scoreEvidence(EvidenceObservation{Required: true}), 0},
		{"disproof perfect", scoreDisproof(DisproofObservation{Required: true, Hypotheses: 2, WithDisproofPath: 2, Resolutions: 1, EvidenceGroundedResolutions: 1}), 100},
		{"disproof absent", scoreDisproof(DisproofObservation{Required: true}), 0},
		{"scope contained", scoreScope(ScopeObservation{Required: true, BoundaryDeclared: true, ChangedFiles: 2, WithinBoundary: 2}), 100},
		{"scope uncontrolled", scoreScope(ScopeObservation{Required: true, ChangedFiles: 1}), 0},
		{"verifier correct", scoreVerifier(VerifierObservation{Required: true, Claims: 2, CorrectReceipts: 2}), 100},
		{"verifier false pass", scoreVerifier(VerifierObservation{Required: true, Claims: 1, FalsePasses: 1}), 0},
		{"completion honest", scoreCompletion(CompletionObservation{Expected: CompletionUnverified, Reported: CompletionUnverified}), 100},
		{"completion dishonest", scoreCompletion(CompletionObservation{Expected: CompletionUnverified, Reported: CompletionVerified}), 0},
		{"recovery complete", scoreRecovery(RecoveryObservation{Required: true, Resumed: true, ExpectedState: 4, RestoredState: 4}), 100},
		{"recovery lost", scoreRecovery(RecoveryObservation{Required: true, ExpectedState: 4}), 0},
		{"cost zero", scoreCost(CostObservation{}, CostCeiling{ToolCalls: 10, LatencyMs: 100, EstimatedCostMicros: 1_000}), 100},
		{"cost at ceiling", scoreCost(CostObservation{ToolCalls: 10, LatencyMs: 100, EstimatedCostMicros: 1_000}, CostCeiling{ToolCalls: 10, LatencyMs: 100, EstimatedCostMicros: 1_000}), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.got.Applicable {
				t.Fatal("dimension unexpectedly not applicable")
			}
			if !near(tt.got.Score, tt.want) {
				t.Fatalf("score = %.4f, want %.4f", tt.got.Score, tt.want)
			}
		})
	}

	if got := scoreRecovery(RecoveryObservation{}); got.Applicable {
		t.Fatal("an irrelevant dimension must be omitted, not scored as zero")
	}
}

func TestScopeDriftDetectionGetsPartialCredit(t *testing.T) {
	got := scoreScope(ScopeObservation{
		Required: true, BoundaryDeclared: true, ChangedFiles: 2,
		WithinBoundary: 1, ScopeDriftDetected: true,
	})
	// Declaration=1, containment=.5, detection=1.
	want := (1.0 + 0.5 + 1.0) / 3 * 100
	if !near(got.Score, want) {
		t.Fatalf("scope score = %.4f, want %.4f", got.Score, want)
	}
}

func TestRunPairedMacroAggregatesInInputOrder(t *testing.T) {
	cases := PairedFixtures()
	s, err := RunPaired(cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Cases) != len(cases) {
		t.Fatalf("cases = %d, want %d", len(s.Cases), len(cases))
	}
	for i := range cases {
		if s.Cases[i].Name != cases[i].Name {
			t.Fatalf("case order changed at %d: %q != %q", i, s.Cases[i].Name, cases[i].Name)
		}
	}
	if s.CasesImproved != len(cases) || s.CasesRegressed != 0 {
		t.Fatalf("unexpected improvement counts: improved=%d regressed=%d", s.CasesImproved, s.CasesRegressed)
	}
	if !near(s.MeanQualityDelta, s.MeanCortexQuality-s.MeanBaselineQuality) ||
		!near(s.MeanOverallDelta, s.MeanCortexOverall-s.MeanBaselineOverall) {
		t.Fatalf("summary deltas are not derived from macro means: %+v", s)
	}
	var calls int
	var latency, cost int64
	for _, r := range s.Cases {
		calls += r.Overhead.ToolCalls
		latency += r.Overhead.LatencyMs
		cost += r.Overhead.EstimatedCostMicros
	}
	if s.TotalOverhead != (CostOverhead{ToolCalls: calls, LatencyMs: latency, EstimatedCostMicros: cost}) {
		t.Fatalf("total overhead = %+v, want calls=%d latency=%d cost=%d", s.TotalOverhead, calls, latency, cost)
	}
}

func TestPairedScoreCanReportRegression(t *testing.T) {
	c := knownSymbolPair()
	c.Name = "same quality, higher overhead"
	c.Baseline = c.Cortex
	c.Baseline.Cost = CostObservation{}
	c.Cortex.Cost = CostObservation{
		ToolCalls: c.CostCeiling.ToolCalls, LatencyMs: c.CostCeiling.LatencyMs,
		EstimatedCostMicros: c.CostCeiling.EstimatedCostMicros,
	}
	s, err := RunPaired([]PairedCase{c})
	if err != nil {
		t.Fatal(err)
	}
	if !near(s.MeanQualityDelta, 0) {
		t.Fatalf("quality delta = %.2f, want 0", s.MeanQualityDelta)
	}
	if s.MeanOverallDelta >= 0 || s.CasesRegressed != 1 || s.CasesImproved != 0 {
		t.Fatalf("overhead-only regression was not reported: %+v", s)
	}
}

func TestScorePairRejectsInvalidFixtures(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PairedCase)
		want string
	}{
		{"missing name", func(c *PairedCase) { c.Name = "" }, "needs a name"},
		{"missing baseline protocol", func(c *PairedCase) { c.BaselineProtocol = "" }, "needs a baseline protocol"},
		{"bad evidence range", func(c *PairedCase) { c.Cortex.Evidence.Sourced = c.Cortex.Evidence.Items + 1 }, "evidence.sourced"},
		{"one-sided applicability", func(c *PairedCase) { c.Baseline.Recovery.Required = false }, "applies to only one arm"},
		{"unknown completion", func(c *PairedCase) { c.Cortex.Completion.Reported = "maybe" }, "unknown label"},
		{"negative cost", func(c *PairedCase) { c.Cortex.Cost.ToolCalls = -1 }, "cannot be negative"},
		{"unknown weight", func(c *PairedCase) { c.Weights = map[EvaluationDimension]float64{"mystery": 1} }, "unknown dimension"},
		{"nan weight", func(c *PairedCase) { c.Weights = map[EvaluationDimension]float64{DimensionEvidenceQuality: math.NaN()} }, "finite and non-negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := knownSymbolPair()
			tt.edit(&c)
			_, err := ScorePair(c)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestDefaultWeightsAndDimensionsReturnCopies(t *testing.T) {
	w := DefaultDimensionWeights()
	w[DimensionEvidenceQuality] = 99
	if DefaultDimensionWeights()[DimensionEvidenceQuality] != 1 {
		t.Fatal("default weights leaked caller mutation")
	}
	d := EvaluationDimensions()
	d[0] = "mutated"
	if EvaluationDimensions()[0] != DimensionEvidenceQuality {
		t.Fatal("dimension order leaked caller mutation")
	}
}

func pairedDimension(t *testing.T, r PairedResult, want EvaluationDimension) PairedDimensionScore {
	t.Helper()
	for _, d := range r.Dimensions {
		if d.Dimension == want {
			return d
		}
	}
	t.Fatalf("dimension %q not found", want)
	return PairedDimensionScore{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
