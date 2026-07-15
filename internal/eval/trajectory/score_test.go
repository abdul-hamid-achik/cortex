package trajectory

import (
	"strings"
	"testing"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

func TestScoreArmsUsesRawToolsAndRetainsIncompleteCandidate(t *testing.T) {
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	instrumented := instrumentedObservation()
	observation := baseeval.TrialObservation{
		Evidence: instrumented.Evidence, Disproof: instrumented.Disproof, Recovery: instrumented.Recovery,
		Scope: baseeval.ScopeObservation{Required: true}, Verifier: baseeval.VerifierObservation{Required: true},
		Completion: baseeval.CompletionObservation{Expected: baseeval.CompletionUnverified, Reported: baseeval.CompletionUnverified},
	}
	arms := []ArmReport{
		{Arm: ArmRawTools, Status: RunFailed, ToolchainValidated: true, Observation: observation},
		{Arm: ArmCortex, Status: RunIncomplete, ToolchainValidated: true, Observation: observation},
	}
	comparisons, warnings, err := ScoreArms(manifest, arms)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 1 || comparisons[0].BaselineArm != ArmRawTools || comparisons[0].CandidateArm != ArmCortex {
		t.Fatalf("comparisons=%+v", comparisons)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "estimated-cost") {
		t.Fatalf("missing-cost warnings=%v", warnings)
	}
	arms[0].Arm = ArmCortex
	if _, _, err := ScoreArms(manifest, arms); err == nil || !strings.Contains(err.Error(), "raw_tools") {
		t.Fatalf("invalid baseline accepted: %v", err)
	}
}

func TestScoreArmsPreservesValidComparisonsWhenOneCandidateIsInvalid(t *testing.T) {
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	valid := baseeval.TrialObservation{
		Completion: baseeval.CompletionObservation{Expected: baseeval.CompletionUnverified, Reported: baseeval.CompletionUnverified},
	}
	invalid := valid
	invalid.Evidence = baseeval.EvidenceObservation{Items: 1, Sourced: 2}
	arms := []ArmReport{
		{Arm: ArmRawTools, ToolchainValidated: true, Observation: valid},
		{Arm: ArmCortex, ToolchainValidated: true, Observation: valid},
		{Arm: ArmCortexBob, ToolchainValidated: true, Observation: invalid},
	}
	comparisons, warnings, err := ScoreArms(manifest, arms)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 1 || comparisons[0].CandidateArm != ArmCortex || len(warnings) < 3 || !strings.Contains(strings.Join(warnings, "\n"), "cortex_bob score unavailable") {
		t.Fatalf("comparisons=%+v warnings=%v", comparisons, warnings)
	}
}
