package eval

const unassistedBaselineProtocol = "same model with direct repository and shell tools; no Cortex case file, gates, or recall"

// PairedFixtures is a deterministic calibration set for the paired scorecard.
// The baseline arm models the same task attempted without Cortex's case file or
// gates; the Cortex arm models the expected observable contract. These fixtures
// test the measurement model and make `task eval` print a stable comparison.
// They are not a statistical claim about every unassisted agent: real repository
// runs can populate PairedCase with recorded observations through the same API.
func PairedFixtures() []PairedCase {
	return []PairedCase{
		knownSymbolPair(),
		misleadingSearchPair(),
		staleIndexPair(),
	}
}

func knownSymbolPair() PairedCase {
	return PairedCase{
		Name:             "known-symbol change",
		Category:         "known-symbol",
		BaselineProtocol: unassistedBaselineProtocol,
		Baseline: TrialObservation{
			Evidence: EvidenceObservation{
				Required: true, Items: 3, Sourced: 2,
				ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 1,
				Candidates: 1, CandidatesKeptAsCandidates: 1,
			},
			Disproof: DisproofObservation{
				Required: true, Hypotheses: 1, WithDisproofPath: 0,
				Resolutions: 1, EvidenceGroundedResolutions: 1,
			},
			Scope: ScopeObservation{
				Required: true, BoundaryDeclared: false, ChangedFiles: 1,
				WithinBoundary: 1, ScopeDriftDetected: false,
			},
			Verifier: VerifierObservation{
				Required: true, Claims: 1, CorrectReceipts: 1,
			},
			Completion: CompletionObservation{Expected: CompletionVerified, Reported: CompletionVerified},
			Recovery:   RecoveryObservation{Required: true, Resumed: false, ExpectedState: 4, RestoredState: 2},
			Cost:       CostObservation{ToolCalls: 1, LatencyMs: 800, EstimatedCostMicros: 8_000},
		},
		Cortex: TrialObservation{
			Evidence: EvidenceObservation{
				Required: true, Items: 4, Sourced: 4,
				ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 1,
				Candidates: 1, CandidatesKeptAsCandidates: 1,
			},
			Disproof: DisproofObservation{
				Required: true, Hypotheses: 1, WithDisproofPath: 1,
				Resolutions: 1, EvidenceGroundedResolutions: 1,
			},
			Scope: ScopeObservation{
				Required: true, BoundaryDeclared: true, ChangedFiles: 1,
				WithinBoundary: 1, ScopeDriftDetected: false,
			},
			Verifier:   VerifierObservation{Required: true, Claims: 1, CorrectReceipts: 1},
			Completion: CompletionObservation{Expected: CompletionVerified, Reported: CompletionVerified},
			Recovery:   RecoveryObservation{Required: true, Resumed: true, ExpectedState: 4, RestoredState: 4},
			Cost:       CostObservation{ToolCalls: 4, LatencyMs: 1_600, EstimatedCostMicros: 6_000},
		},
		CostCeiling: CostCeiling{ToolCalls: 10, LatencyMs: 5_000, EstimatedCostMicros: 10_000},
	}
}

func misleadingSearchPair() PairedCase {
	return PairedCase{
		Name:             "misleading search candidate",
		Category:         "misleading-search",
		BaselineProtocol: unassistedBaselineProtocol,
		Baseline: TrialObservation{
			Evidence: EvidenceObservation{
				Required: true, Items: 1, Sourced: 1,
				ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 0,
				Candidates: 1, CandidatesKeptAsCandidates: 0,
			},
			Disproof: DisproofObservation{Required: true, Hypotheses: 1},
			Scope: ScopeObservation{
				Required: true, BoundaryDeclared: false, ChangedFiles: 1,
				WithinBoundary: 0, ScopeDriftDetected: false,
			},
			Verifier:   VerifierObservation{Required: true, Claims: 1, FalsePasses: 1},
			Completion: CompletionObservation{Expected: CompletionUnverified, Reported: CompletionVerified},
			Recovery:   RecoveryObservation{Required: true, Resumed: false, ExpectedState: 3, RestoredState: 0},
			Cost:       CostObservation{ToolCalls: 1, LatencyMs: 600, EstimatedCostMicros: 7_000},
		},
		Cortex: TrialObservation{
			Evidence: EvidenceObservation{
				Required: true, Items: 3, Sourced: 3,
				ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 1,
				Candidates: 1, CandidatesKeptAsCandidates: 1,
			},
			Disproof: DisproofObservation{
				Required: true, Hypotheses: 1, WithDisproofPath: 1,
				Resolutions: 1, EvidenceGroundedResolutions: 1,
			},
			Scope: ScopeObservation{
				Required: true, BoundaryDeclared: true, ChangedFiles: 1,
				WithinBoundary: 1, ScopeDriftDetected: false,
			},
			Verifier:   VerifierObservation{Required: true, Claims: 1, CorrectReceipts: 1},
			Completion: CompletionObservation{Expected: CompletionVerified, Reported: CompletionVerified},
			Recovery:   RecoveryObservation{Required: true, Resumed: true, ExpectedState: 3, RestoredState: 3},
			Cost:       CostObservation{ToolCalls: 5, LatencyMs: 1_900, EstimatedCostMicros: 6_500},
		},
		CostCeiling: CostCeiling{ToolCalls: 10, LatencyMs: 5_000, EstimatedCostMicros: 10_000},
	}
}

func staleIndexPair() PairedCase {
	return PairedCase{
		Name:             "stale index degradation",
		Category:         "stale-index",
		BaselineProtocol: unassistedBaselineProtocol,
		Baseline: TrialObservation{
			Evidence: EvidenceObservation{
				Required: true, Items: 1, Sourced: 1,
				ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 0,
			},
			Disproof: DisproofObservation{Required: true, Hypotheses: 1, WithDisproofPath: 1},
			Scope: ScopeObservation{
				Required: true, BoundaryDeclared: true, ChangedFiles: 1,
				WithinBoundary: 1, ScopeDriftDetected: false,
			},
			// The unassisted arm is honest about the stale index, but has no durable
			// receipt. Integrity gets credit; receipt coverage does not.
			Verifier:   VerifierObservation{Required: true, Claims: 1},
			Completion: CompletionObservation{Expected: CompletionUnverified, Reported: CompletionUnverified},
			Recovery:   RecoveryObservation{Required: true, Resumed: false, ExpectedState: 4, RestoredState: 1},
			Cost:       CostObservation{ToolCalls: 1, LatencyMs: 500, EstimatedCostMicros: 5_500},
		},
		Cortex: TrialObservation{
			Evidence: EvidenceObservation{
				Required: true, Items: 2, Sourced: 2,
				ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 0,
			},
			Disproof: DisproofObservation{Required: true, Hypotheses: 1, WithDisproofPath: 1},
			Scope: ScopeObservation{
				Required: true, BoundaryDeclared: true, ChangedFiles: 1,
				WithinBoundary: 1, ScopeDriftDetected: false,
			},
			// A truthful blocked/not_run receipt is correct even though it is not a pass.
			Verifier:   VerifierObservation{Required: true, Claims: 1, CorrectReceipts: 1},
			Completion: CompletionObservation{Expected: CompletionUnverified, Reported: CompletionUnverified},
			Recovery:   RecoveryObservation{Required: true, Resumed: true, ExpectedState: 4, RestoredState: 4},
			Cost:       CostObservation{ToolCalls: 3, LatencyMs: 1_100, EstimatedCostMicros: 4_500},
		},
		CostCeiling: CostCeiling{ToolCalls: 10, LatencyMs: 5_000, EstimatedCostMicros: 10_000},
	}
}
