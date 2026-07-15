package trajectory

import (
	"fmt"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

// ScoreArms reuses Cortex's deterministic measurement model while keeping the
// empirical oracle result separately visible in each ArmReport. Raw tools is
// always the baseline; failed/incomplete arms remain present and scoreable.
func ScoreArms(manifest Manifest, arms []ArmReport) ([]ArmComparison, []string, error) {
	if len(arms) < 2 || arms[0].Arm != ArmRawTools {
		return nil, nil, fmt.Errorf("trajectory scoring needs raw_tools followed by candidate arms")
	}
	comparisons := make([]ArmComparison, 0, len(arms)-1)
	var warnings []string
	if !arms[0].ToolchainValidated {
		return nil, []string{"all score comparisons omitted because raw_tools toolchain provenance was not validated"}, nil
	}
	for _, candidate := range arms[1:] {
		if !candidate.ToolchainValidated {
			warnings = append(warnings, fmt.Sprintf("%s score omitted because toolchain provenance was not validated", candidate.Arm))
			continue
		}
		baselineObservation := arms[0].Observation
		candidateObservation := candidate.Observation
		ceiling := baseeval.CostCeiling{
			ToolCalls:           manifest.Budget.MaxToolCalls,
			LatencyMs:           manifest.Budget.MaxWallTime.Value().Milliseconds(),
			EstimatedCostMicros: manifest.Budget.MaxEstimatedCostMicros,
		}
		if arms[0].EstimatedCostMicros == nil || candidate.EstimatedCostMicros == nil {
			ceiling.EstimatedCostMicros = 0
			baselineObservation.Cost.EstimatedCostMicros = 0
			candidateObservation.Cost.EstimatedCostMicros = 0
			warnings = append(warnings, fmt.Sprintf("%s estimated-cost comparison omitted because one or both arms did not report cost", candidate.Arm))
		}
		score, err := baseeval.ScorePair(baseeval.PairedCase{
			Name: manifest.ID + "/" + string(candidate.Arm), Category: "empirical_trajectory",
			BaselineProtocol: "fixed manifest v1; raw_tools baseline",
			Baseline:         baselineObservation, Cortex: candidateObservation, CostCeiling: ceiling,
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s score unavailable: %v", candidate.Arm, err))
			continue
		}
		comparisons = append(comparisons, ArmComparison{
			BaselineArm: ArmRawTools, CandidateArm: candidate.Arm, Score: score,
		})
	}
	return comparisons, warnings, nil
}
