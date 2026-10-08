package domain

import "testing"

// An attestation is never verifier proof, whatever its status: no caller can
// launder it into a pass or a definitive failure.
func TestAttestationIsNeverVerifierProof(t *testing.T) {
	attested := VerificationRecord{Claim: "no commit is made", Purpose: VerificationPurposeAttestation,
		Status: VerifyPassed, Binding: VerificationBound, Evidence: []string{"ev_1"}}
	if err := attested.Validate(); err != nil {
		t.Fatalf("attestation should validate: %v", err)
	}
	if attested.Proven() || attested.Definitive() {
		t.Fatal("an attestation must never be Proven or Definitive")
	}
	if !attested.Attested() {
		t.Fatal("a bound, evidence-backed attestation should be Attested")
	}
	failed := attested
	failed.Status = VerifyFailed
	if failed.Failed() || failed.Attested() {
		t.Fatal("an attestation never carries a definitive failure either")
	}
	bare := attested
	bare.Evidence = nil
	if bare.Attested() {
		t.Fatal("an attestation without evidence refs is not an attestation")
	}
	named := attested
	named.Purpose = VerificationPurposeNamedClaim
	if !named.Proven() || named.Attested() {
		t.Fatal("ordinary named-claim proof must be unaffected")
	}
}

func TestCriterionKindIsProcessOrEmpty(t *testing.T) {
	if err := (AcceptanceCriterion{ID: "a", Statement: "s", Kind: CriterionKindProcess}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AcceptanceCriterion{ID: "a", Statement: "s", Kind: "behavioral"}).Validate(); err == nil {
		t.Fatal("unknown criterion kinds must be rejected")
	}
}
