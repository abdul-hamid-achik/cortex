package eval

import (
	"fmt"
	"math"
)

// EvaluationDimension is one quality or guardrail axis in a paired evaluation.
// Scores are normalized to 0..100 and always point in the same direction:
// higher is better. CostLatencyOverhead is intentionally a lower-weight
// guardrail in DefaultDimensionWeights; raw overhead is also reported.
type EvaluationDimension string

const (
	DimensionEvidenceQuality      EvaluationDimension = "evidence_quality"
	DimensionDisproofDiscipline   EvaluationDimension = "disproof_discipline"
	DimensionBoundaryScopeControl EvaluationDimension = "boundary_scope_control"
	DimensionVerifierCorrectness  EvaluationDimension = "verifier_correctness"
	DimensionCompletionHonesty    EvaluationDimension = "completion_honesty"
	DimensionRecoveryResume       EvaluationDimension = "recovery_resume"
	DimensionCostLatencyOverhead  EvaluationDimension = "cost_latency_overhead"
)

var evaluationDimensions = []EvaluationDimension{
	DimensionEvidenceQuality,
	DimensionDisproofDiscipline,
	DimensionBoundaryScopeControl,
	DimensionVerifierCorrectness,
	DimensionCompletionHonesty,
	DimensionRecoveryResume,
	DimensionCostLatencyOverhead,
}

var qualityDimensions = []EvaluationDimension{
	DimensionEvidenceQuality,
	DimensionDisproofDiscipline,
	DimensionBoundaryScopeControl,
	DimensionVerifierCorrectness,
	DimensionCompletionHonesty,
	DimensionRecoveryResume,
}

// EvaluationDimensions returns the stable display/scoring order. The returned
// slice is a copy so callers cannot mutate package policy.
func EvaluationDimensions() []EvaluationDimension {
	return append([]EvaluationDimension(nil), evaluationDimensions...)
}

// DefaultDimensionWeights keeps the six quality dimensions equally important
// and treats cost/latency as a guardrail: overhead matters, but cannot outweigh
// an otherwise large gain in correctness and honesty by itself.
func DefaultDimensionWeights() map[EvaluationDimension]float64 {
	return map[EvaluationDimension]float64{
		DimensionEvidenceQuality:      1,
		DimensionDisproofDiscipline:   1,
		DimensionBoundaryScopeControl: 1,
		DimensionVerifierCorrectness:  1,
		DimensionCompletionHonesty:    1,
		DimensionRecoveryResume:       1,
		DimensionCostLatencyOverhead:  0.5,
	}
}

// EvidenceObservation measures provenance, proof coverage, and whether search
// candidates stayed candidates. ClaimsRequiringProof is the denominator only
// for definitive claims; ordinary discovery candidates need not be verifiable.
type EvidenceObservation struct {
	Required                   bool `json:"required"`
	Items                      int  `json:"items"`
	Sourced                    int  `json:"sourced"`
	ClaimsRequiringProof       int  `json:"claimsRequiringProof"`
	ClaimsWithVerifiableSource int  `json:"claimsWithVerifiableSource"`
	Candidates                 int  `json:"candidates"`
	CandidatesKeptAsCandidates int  `json:"candidatesKeptAsCandidates"`
}

// DisproofObservation measures falsifiability and whether status changes were
// grounded in evidence instead of rhetorical confidence.
type DisproofObservation struct {
	Required                    bool `json:"required"`
	Hypotheses                  int  `json:"hypotheses"`
	WithDisproofPath            int  `json:"withDisproofPath"`
	Resolutions                 int  `json:"resolutions"`
	EvidenceGroundedResolutions int  `json:"evidenceGroundedResolutions"`
}

// ScopeObservation measures both prevention and detection: declaring a
// boundary, staying within it, and surfacing any actual drift.
type ScopeObservation struct {
	Required           bool `json:"required"`
	BoundaryDeclared   bool `json:"boundaryDeclared"`
	ChangedFiles       int  `json:"changedFiles"`
	WithinBoundary     int  `json:"withinBoundary"`
	ScopeDriftDetected bool `json:"scopeDriftDetected"`
}

// VerifierObservation scores claim-to-verifier mapping and receipt truth. A
// CorrectReceipt can be a truthful pass, failure, blocked, or not_run result;
// FalsePasses and StalePasses are integrity failures.
type VerifierObservation struct {
	Required        bool `json:"required"`
	Claims          int  `json:"claims"`
	CorrectReceipts int  `json:"correctReceipts"`
	FalsePasses     int  `json:"falsePasses"`
	StalePasses     int  `json:"stalePasses"`
}

// CompletionLabel is the externally visible completion assessment.
type CompletionLabel string

const (
	CompletionIncomplete CompletionLabel = "incomplete"
	CompletionVerified   CompletionLabel = "verified"
	CompletionUnverified CompletionLabel = "unverified"
	CompletionFailed     CompletionLabel = "failed"
)

// CompletionObservation compares the honest label implied by the run with the
// label it actually reported. Expected may differ by arm: if no verifier ran,
// "unverified" is honest even when the task's desired outcome was success.
type CompletionObservation struct {
	Expected CompletionLabel `json:"expected"`
	Reported CompletionLabel `json:"reported"`
}

// RecoveryObservation measures whether an interrupted run resumed and how much
// decision-relevant state survived (goal, evidence, hypothesis, boundary, etc.).
type RecoveryObservation struct {
	Required      bool `json:"required"`
	Resumed       bool `json:"resumed"`
	ExpectedState int  `json:"expectedState"`
	RestoredState int  `json:"restoredState"`
}

// CostObservation uses integer units so fixtures and scorecards are stable.
// EstimatedCostMicros is an optional provider/gateway cost estimate in millionths
// of the configured currency unit.
type CostObservation struct {
	ToolCalls           int   `json:"toolCalls"`
	LatencyMs           int64 `json:"latencyMs"`
	EstimatedCostMicros int64 `json:"estimatedCostMicros"`
}

// CostCeiling is the zero-score ceiling for each overhead component. A zero
// ceiling omits that component; zero usage scores 100 and usage at/above the
// ceiling scores 0. The paired result also reports raw deltas.
type CostCeiling struct {
	ToolCalls           int   `json:"toolCalls"`
	LatencyMs           int64 `json:"latencyMs"`
	EstimatedCostMicros int64 `json:"estimatedCostMicros"`
}

// TrialObservation is the structured record for one arm of a paired case.
type TrialObservation struct {
	Evidence   EvidenceObservation   `json:"evidence"`
	Disproof   DisproofObservation   `json:"disproof"`
	Scope      ScopeObservation      `json:"scope"`
	Verifier   VerifierObservation   `json:"verifier"`
	Completion CompletionObservation `json:"completion"`
	Recovery   RecoveryObservation   `json:"recovery"`
	Cost       CostObservation       `json:"cost"`
}

// PairedCase compares Cortex with an unassisted baseline on the same task and
// oracle. BaselineProtocol makes the comparison reproducible instead of treating
// "unassisted" as an implicit, moving target. Weights override selected defaults;
// omitted weights keep the default.
type PairedCase struct {
	Name             string                          `json:"name"`
	Category         string                          `json:"category"`
	BaselineProtocol string                          `json:"baselineProtocol"`
	Baseline         TrialObservation                `json:"baseline"`
	Cortex           TrialObservation                `json:"cortex"`
	CostCeiling      CostCeiling                     `json:"costCeiling"`
	Weights          map[EvaluationDimension]float64 `json:"weights,omitempty"`
}

// DimensionScore is one arm's normalized result. Applicable=false keeps a
// dimension out of weighted totals instead of treating "not relevant" as zero.
type DimensionScore struct {
	Applicable bool    `json:"applicable"`
	Score      float64 `json:"score"`
}

// PairedDimensionScore is the per-dimension lift for one case.
type PairedDimensionScore struct {
	Dimension EvaluationDimension `json:"dimension"`
	Weight    float64             `json:"weight"`
	Baseline  float64             `json:"baseline"`
	Cortex    float64             `json:"cortex"`
	Delta     float64             `json:"delta"`
}

// CostOverhead is Cortex minus baseline in raw units. Positive values mean
// additional overhead; negative values mean Cortex used less.
type CostOverhead struct {
	ToolCalls           int   `json:"toolCalls"`
	LatencyMs           int64 `json:"latencyMs"`
	EstimatedCostMicros int64 `json:"estimatedCostMicros"`
}

// PairedResult is a decision-oriented comparison for one task. Quality excludes
// the cost guardrail; Overall includes it with its configured weight.
type PairedResult struct {
	Name             string                 `json:"name"`
	Category         string                 `json:"category"`
	BaselineProtocol string                 `json:"baselineProtocol"`
	Dimensions       []PairedDimensionScore `json:"dimensions"`
	BaselineQuality  float64                `json:"baselineQuality"`
	CortexQuality    float64                `json:"cortexQuality"`
	QualityDelta     float64                `json:"qualityDelta"`
	BaselineOverall  float64                `json:"baselineOverall"`
	CortexOverall    float64                `json:"cortexOverall"`
	OverallDelta     float64                `json:"overallDelta"`
	Overhead         CostOverhead           `json:"overhead"`
}

// PairedSummary aggregates deterministic paired cases. Means are macro-averages
// so a case with more claims does not silently dominate the benchmark.
type PairedSummary struct {
	Cases               []PairedResult `json:"cases"`
	MeanBaselineQuality float64        `json:"meanBaselineQuality"`
	MeanCortexQuality   float64        `json:"meanCortexQuality"`
	MeanQualityDelta    float64        `json:"meanQualityDelta"`
	MeanBaselineOverall float64        `json:"meanBaselineOverall"`
	MeanCortexOverall   float64        `json:"meanCortexOverall"`
	MeanOverallDelta    float64        `json:"meanOverallDelta"`
	CasesImproved       int            `json:"casesImproved"`
	CasesRegressed      int            `json:"casesRegressed"`
	TotalOverhead       CostOverhead   `json:"totalOverhead"`
}

// ScorePair validates and scores one baseline/Cortex pair.
func ScorePair(c PairedCase) (PairedResult, error) {
	if err := c.validate(); err != nil {
		return PairedResult{}, err
	}
	weights := DefaultDimensionWeights()
	for d, w := range c.Weights {
		weights[d] = w
	}
	baseline := scoreTrial(c.Baseline, c.CostCeiling)
	cortex := scoreTrial(c.Cortex, c.CostCeiling)

	r := PairedResult{Name: c.Name, Category: c.Category, BaselineProtocol: c.BaselineProtocol}
	for _, d := range evaluationDimensions {
		b, x := baseline[d], cortex[d]
		if b.Applicable != x.Applicable {
			return PairedResult{}, fmt.Errorf("paired case %q dimension %q applies to only one arm", c.Name, d)
		}
		if !b.Applicable || weights[d] == 0 {
			continue
		}
		r.Dimensions = append(r.Dimensions, PairedDimensionScore{
			Dimension: d, Weight: weights[d], Baseline: b.Score, Cortex: x.Score, Delta: x.Score - b.Score,
		})
	}
	r.BaselineQuality = weightedScore(r.Dimensions, qualityDimensions, false)
	r.CortexQuality = weightedScore(r.Dimensions, qualityDimensions, true)
	r.QualityDelta = r.CortexQuality - r.BaselineQuality
	r.BaselineOverall = weightedScore(r.Dimensions, evaluationDimensions, false)
	r.CortexOverall = weightedScore(r.Dimensions, evaluationDimensions, true)
	r.OverallDelta = r.CortexOverall - r.BaselineOverall
	r.Overhead = CostOverhead{
		ToolCalls:           c.Cortex.Cost.ToolCalls - c.Baseline.Cost.ToolCalls,
		LatencyMs:           c.Cortex.Cost.LatencyMs - c.Baseline.Cost.LatencyMs,
		EstimatedCostMicros: c.Cortex.Cost.EstimatedCostMicros - c.Baseline.Cost.EstimatedCostMicros,
	}
	return r, nil
}

// RunPaired scores and macro-aggregates a set of paired cases in input order.
func RunPaired(cases []PairedCase) (PairedSummary, error) {
	s := PairedSummary{Cases: make([]PairedResult, 0, len(cases))}
	for _, c := range cases {
		r, err := ScorePair(c)
		if err != nil {
			return PairedSummary{}, err
		}
		s.Cases = append(s.Cases, r)
		s.MeanBaselineQuality += r.BaselineQuality
		s.MeanCortexQuality += r.CortexQuality
		s.MeanBaselineOverall += r.BaselineOverall
		s.MeanCortexOverall += r.CortexOverall
		s.TotalOverhead.ToolCalls += r.Overhead.ToolCalls
		s.TotalOverhead.LatencyMs += r.Overhead.LatencyMs
		s.TotalOverhead.EstimatedCostMicros += r.Overhead.EstimatedCostMicros
		switch {
		case r.OverallDelta > 0:
			s.CasesImproved++
		case r.OverallDelta < 0:
			s.CasesRegressed++
		}
	}
	if len(s.Cases) > 0 {
		n := float64(len(s.Cases))
		s.MeanBaselineQuality /= n
		s.MeanCortexQuality /= n
		s.MeanBaselineOverall /= n
		s.MeanCortexOverall /= n
	}
	s.MeanQualityDelta = s.MeanCortexQuality - s.MeanBaselineQuality
	s.MeanOverallDelta = s.MeanCortexOverall - s.MeanBaselineOverall
	return s, nil
}

func scoreTrial(o TrialObservation, ceiling CostCeiling) map[EvaluationDimension]DimensionScore {
	return map[EvaluationDimension]DimensionScore{
		DimensionEvidenceQuality:      scoreEvidence(o.Evidence),
		DimensionDisproofDiscipline:   scoreDisproof(o.Disproof),
		DimensionBoundaryScopeControl: scoreScope(o.Scope),
		DimensionVerifierCorrectness:  scoreVerifier(o.Verifier),
		DimensionCompletionHonesty:    scoreCompletion(o.Completion),
		DimensionRecoveryResume:       scoreRecovery(o.Recovery),
		DimensionCostLatencyOverhead:  scoreCost(o.Cost, ceiling),
	}
}

func scoreEvidence(o EvidenceObservation) DimensionScore {
	if !o.Required {
		return DimensionScore{}
	}
	if o.Items == 0 {
		return applicable(0)
	}
	parts := []float64{ratio(o.Sourced, o.Items)}
	if o.ClaimsRequiringProof > 0 {
		parts = append(parts, ratio(o.ClaimsWithVerifiableSource, o.ClaimsRequiringProof))
	}
	if o.Candidates > 0 {
		parts = append(parts, ratio(o.CandidatesKeptAsCandidates, o.Candidates))
	}
	return applicable(mean(parts) * 100)
}

func scoreDisproof(o DisproofObservation) DimensionScore {
	if !o.Required {
		return DimensionScore{}
	}
	if o.Hypotheses == 0 {
		return applicable(0)
	}
	parts := []float64{ratio(o.WithDisproofPath, o.Hypotheses)}
	if o.Resolutions > 0 {
		parts = append(parts, ratio(o.EvidenceGroundedResolutions, o.Resolutions))
	}
	return applicable(mean(parts) * 100)
}

func scoreScope(o ScopeObservation) DimensionScore {
	if !o.Required {
		return DimensionScore{}
	}
	declared := 0.0
	if o.BoundaryDeclared {
		declared = 1
	}
	containment := 1.0
	if o.ChangedFiles > 0 {
		containment = ratio(o.WithinBoundary, o.ChangedFiles)
	}
	detection := 1.0
	if o.ChangedFiles-o.WithinBoundary > 0 && !o.ScopeDriftDetected {
		detection = 0
	}
	return applicable(mean([]float64{declared, containment, detection}) * 100)
}

func scoreVerifier(o VerifierObservation) DimensionScore {
	if !o.Required {
		return DimensionScore{}
	}
	if o.Claims == 0 {
		return applicable(0)
	}
	coverage := ratio(o.CorrectReceipts, o.Claims)
	integrity := 1 - ratio(min(o.Claims, o.FalsePasses+o.StalePasses), o.Claims)
	return applicable(mean([]float64{coverage, integrity}) * 100)
}

func scoreCompletion(o CompletionObservation) DimensionScore {
	if o.Expected == "" {
		return DimensionScore{}
	}
	if o.Expected == o.Reported {
		return applicable(100)
	}
	return applicable(0)
}

func scoreRecovery(o RecoveryObservation) DimensionScore {
	if !o.Required {
		return DimensionScore{}
	}
	resumed := 0.0
	if o.Resumed {
		resumed = 1
	}
	state := 1.0
	if o.ExpectedState > 0 {
		state = ratio(o.RestoredState, o.ExpectedState)
	}
	return applicable(mean([]float64{resumed, state}) * 100)
}

func scoreCost(o CostObservation, c CostCeiling) DimensionScore {
	var parts []float64
	if c.ToolCalls > 0 {
		parts = append(parts, ceilingScore(int64(o.ToolCalls), int64(c.ToolCalls)))
	}
	if c.LatencyMs > 0 {
		parts = append(parts, ceilingScore(o.LatencyMs, c.LatencyMs))
	}
	if c.EstimatedCostMicros > 0 {
		parts = append(parts, ceilingScore(o.EstimatedCostMicros, c.EstimatedCostMicros))
	}
	if len(parts) == 0 {
		return DimensionScore{}
	}
	return applicable(mean(parts) * 100)
}

func applicable(score float64) DimensionScore {
	return DimensionScore{Applicable: true, Score: clamp(score, 0, 100)}
}

func ratio(n, d int) float64 {
	if d <= 0 {
		return 0
	}
	return clamp(float64(n)/float64(d), 0, 1)
}

func ceilingScore(actual, ceiling int64) float64 {
	if ceiling <= 0 {
		return 0
	}
	return 1 - clamp(float64(actual)/float64(ceiling), 0, 1)
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

func weightedScore(scores []PairedDimensionScore, include []EvaluationDimension, cortex bool) float64 {
	allowed := make(map[EvaluationDimension]bool, len(include))
	for _, d := range include {
		allowed[d] = true
	}
	var weighted, weights float64
	for _, d := range scores {
		if !allowed[d.Dimension] || d.Weight <= 0 {
			continue
		}
		v := d.Baseline
		if cortex {
			v = d.Cortex
		}
		weighted += v * d.Weight
		weights += d.Weight
	}
	if weights == 0 {
		return 0
	}
	return weighted / weights
}

func (c PairedCase) validate() error {
	if c.Name == "" {
		return fmt.Errorf("paired case needs a name")
	}
	if c.BaselineProtocol == "" {
		return fmt.Errorf("paired case %q needs a baseline protocol", c.Name)
	}
	if err := validateObservation("baseline", c.Baseline); err != nil {
		return fmt.Errorf("paired case %q: %w", c.Name, err)
	}
	if err := validateObservation("cortex", c.Cortex); err != nil {
		return fmt.Errorf("paired case %q: %w", c.Name, err)
	}
	if c.CostCeiling.ToolCalls < 0 || c.CostCeiling.LatencyMs < 0 || c.CostCeiling.EstimatedCostMicros < 0 {
		return fmt.Errorf("paired case %q: cost ceilings cannot be negative", c.Name)
	}
	valid := map[EvaluationDimension]bool{}
	for _, d := range evaluationDimensions {
		valid[d] = true
	}
	for d, w := range c.Weights {
		if !valid[d] {
			return fmt.Errorf("paired case %q: unknown dimension %q", c.Name, d)
		}
		if w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			return fmt.Errorf("paired case %q: weight for %q must be finite and non-negative", c.Name, d)
		}
	}
	return nil
}

func validateObservation(arm string, o TrialObservation) error {
	if err := validateRange("evidence.sourced", o.Evidence.Sourced, o.Evidence.Items); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("evidence.claimsWithVerifiableSource", o.Evidence.ClaimsWithVerifiableSource, o.Evidence.ClaimsRequiringProof); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("evidence.candidatesKeptAsCandidates", o.Evidence.CandidatesKeptAsCandidates, o.Evidence.Candidates); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("disproof.withDisproofPath", o.Disproof.WithDisproofPath, o.Disproof.Hypotheses); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("disproof.evidenceGroundedResolutions", o.Disproof.EvidenceGroundedResolutions, o.Disproof.Resolutions); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("scope.withinBoundary", o.Scope.WithinBoundary, o.Scope.ChangedFiles); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("verifier.correctReceipts", o.Verifier.CorrectReceipts, o.Verifier.Claims); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("verifier.falsePasses", o.Verifier.FalsePasses, o.Verifier.Claims); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("verifier.stalePasses", o.Verifier.StalePasses, o.Verifier.Claims); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if err := validateRange("recovery.restoredState", o.Recovery.RestoredState, o.Recovery.ExpectedState); err != nil {
		return fmt.Errorf("%s: %w", arm, err)
	}
	if o.Cost.ToolCalls < 0 || o.Cost.LatencyMs < 0 || o.Cost.EstimatedCostMicros < 0 {
		return fmt.Errorf("%s: cost observations cannot be negative", arm)
	}
	if err := validateCompletion(o.Completion.Expected); err != nil {
		return fmt.Errorf("%s expected completion: %w", arm, err)
	}
	if err := validateCompletion(o.Completion.Reported); err != nil {
		return fmt.Errorf("%s reported completion: %w", arm, err)
	}
	if (o.Completion.Expected == "") != (o.Completion.Reported == "") {
		return fmt.Errorf("%s: expected and reported completion must both be set or both be empty", arm)
	}
	return nil
}

func validateRange(name string, value, total int) error {
	if value < 0 || total < 0 || value > total {
		return fmt.Errorf("%s must satisfy 0 <= value <= total (got %d/%d)", name, value, total)
	}
	return nil
}

func validateCompletion(v CompletionLabel) error {
	switch v {
	case "", CompletionIncomplete, CompletionVerified, CompletionUnverified, CompletionFailed:
		return nil
	default:
		return fmt.Errorf("unknown label %q", v)
	}
}
