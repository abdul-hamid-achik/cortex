package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvidenceValidate(t *testing.T) {
	base := Evidence{Claim: "x", Source: Source{Tool: "codemap"}, Timestamp: time.Now()}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	if err := (Evidence{Source: Source{Tool: "x"}, Timestamp: time.Now()}).Validate(); err == nil {
		t.Error("evidence with no claim should be invalid")
	}
	if err := (Evidence{Claim: "x", Timestamp: time.Now()}).Validate(); err == nil {
		t.Error("evidence with no source should be invalid")
	}
	if err := (Evidence{Claim: "x", Source: Source{Origin: "human"}}).Validate(); err == nil {
		t.Error("evidence with no timestamp should be invalid")
	}
	// Human-origin evidence is valid without a tool.
	if err := (Evidence{Claim: "x", Source: Source{Origin: "human"}, Timestamp: time.Now()}).Validate(); err != nil {
		t.Errorf("human-origin evidence should be valid: %v", err)
	}
}

func TestEvidenceKindCanVerify(t *testing.T) {
	// model_inference and human_report must NOT satisfy verification alone.
	for _, k := range []EvidenceKind{KindModelInference, KindHumanReport, KindSemanticSearch} {
		if k.CanVerify() {
			t.Errorf("%s should not satisfy verification by itself", k)
		}
	}
	for _, k := range []EvidenceKind{KindBrowserRun, KindTerminalRun, KindUnitTest, KindCodeGraph} {
		if !k.CanVerify() {
			t.Errorf("%s should be able to satisfy verification", k)
		}
	}
}

func TestHypothesisValidate_RequiresDisproof(t *testing.T) {
	noDisproof := Hypothesis{Statement: "returnTo is dropped"}
	if err := noDisproof.Validate(); err == nil {
		t.Error("hypothesis without a disproof path must be rejected (planning gate)")
	}
	withDisproof := Hypothesis{Statement: "returnTo is dropped", DisproveBy: Disproof{Note: "run browser flow"}}
	if err := withDisproof.Validate(); err != nil {
		t.Errorf("hypothesis with disproof should be valid: %v", err)
	}
	if (Hypothesis{Statement: "x"}).DisproveBy.Declared() {
		t.Error("empty disproof should not be declared")
	}
}

func TestPlanValidate(t *testing.T) {
	good := Plan{
		Hypotheses:  []Hypothesis{{Statement: "h", DisproveBy: Disproof{Note: "d"}}},
		Uncertainty: "unsure about signing",
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	if err := (Plan{Uncertainty: "x"}).Validate(); err == nil {
		t.Error("plan with no hypotheses should be invalid")
	}
	noUncertainty := Plan{Hypotheses: []Hypothesis{{Statement: "h", DisproveBy: Disproof{Note: "d"}}}}
	if err := noUncertainty.Validate(); err == nil {
		t.Error("plan must state uncertainty explicitly")
	}
	badHyp := Plan{Hypotheses: []Hypothesis{{Statement: "h"}}, Uncertainty: "x"}
	if err := badHyp.Validate(); err == nil {
		t.Error("plan with a disproof-less hypothesis should be invalid")
	}
	symbolOnly := good
	symbolOnly.ChangeBoundary = ChangeBoundary{Symbols: []string{"HandleCallback"}}
	if err := symbolOnly.Validate(); err == nil {
		t.Error("symbol-only boundaries must be rejected until symbol drift is implemented")
	}
	unknownVerifier := good
	unknownVerifier.VerificationRequired = []string{"custom_shell_check"}
	if err := unknownVerifier.Validate(); err == nil {
		t.Error("unknown verification requirement should be invalid")
	}
	knownVerifiers := good
	knownVerifiers.VerificationRequired = []string{
		"codemap_review", "cairntrace_flow", "glyphrun_flow", "fcheap_artifact", "tvault_capability",
	}
	if err := knownVerifiers.Validate(); err != nil {
		t.Errorf("known verification requirements rejected: %v", err)
	}
}

func TestVerificationRecordValidate(t *testing.T) {
	if err := (VerificationRecord{Claim: "c", Status: VerifyPassed}).Validate(); err != nil {
		t.Errorf("valid record rejected: %v", err)
	}
	if err := (VerificationRecord{Status: VerifyPassed}).Validate(); err == nil {
		t.Error("record with no claim should be invalid")
	}
	if err := (VerificationRecord{Claim: "c"}).Validate(); err == nil {
		t.Error("record with no status should be invalid")
	}
	if !(VerificationRecord{Status: VerifyPassed}).Proven() {
		t.Error("passed record should report proven")
	}
	if (VerificationRecord{Status: VerifyNotRun}).Proven() {
		t.Error("not_run must never report proven")
	}
	if err := (VerificationRecord{Claim: "c", Status: VerifyPassed, Purpose: "other"}).Validate(); err == nil {
		t.Error("unknown verification purpose should be invalid")
	}
}

func TestVerificationClaimValidate(t *testing.T) {
	for _, claim := range []VerificationClaim{
		{Statement: "callback compiles", Surface: SurfaceCode, Verifier: "codemap"},
		{Statement: "unit suite passes", Surface: SurfaceCode, Verifier: "command:unit"},
		{Statement: "redirect works", Surface: SurfaceBrowser, Verifier: "cairntrace"},
	} {
		if err := claim.Validate(); err != nil {
			t.Errorf("valid claim rejected (%+v): %v", claim, err)
		}
	}
	for _, claim := range []VerificationClaim{
		{Statement: "", Surface: SurfaceCode},
		{Statement: "x", Surface: "mobile"},
		{Statement: "login parser compiles", Surface: SurfaceCode, Verifier: "cairntrace"},
		{Statement: "redirect works", Surface: SurfaceBrowser, Verifier: "command:unit"},
	} {
		if err := claim.Validate(); err == nil {
			t.Errorf("invalid claim accepted: %+v", claim)
		}
	}
}

func TestVerificationPurposeLegacyCompatibility(t *testing.T) {
	var verifier VerificationRecord
	if err := json.Unmarshal([]byte(`{"claim":"structural review of the diff","surface":"code","status":"passed"}`), &verifier); err != nil {
		t.Fatal(err)
	}
	if verifier.EffectivePurpose() != VerificationPurposeVerifierRun {
		t.Fatalf("legacy structural receipt purpose = %q, want verifier_run", verifier.EffectivePurpose())
	}

	var claim VerificationRecord
	if err := json.Unmarshal([]byte(`{"claim":"the redirect returns to checkout","surface":"browser","tool":"cairntrace","status":"not_run"}`), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.EffectivePurpose() != VerificationPurposeNamedClaim {
		t.Fatalf("legacy named-claim receipt purpose = %q, want named_claim", claim.EffectivePurpose())
	}
}
