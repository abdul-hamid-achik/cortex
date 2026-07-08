package domain

import (
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
}
