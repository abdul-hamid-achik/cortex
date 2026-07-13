package kernel

import (
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestAssessCaseVerificationKeepsLegacySemanticsWithoutCriteria(t *testing.T) {
	c := &domain.CaseFile{VerificationRequired: []string{"codemap_review"}}
	receipts := []domain.VerificationRecord{{
		Claim: "structural review", Surface: domain.SurfaceCode, Tool: "codemap",
		Purpose: domain.VerificationPurposeVerifierRun, Requirement: "codemap_review", Status: domain.VerifyPassed,
	}}
	want := assessVerification(c.VerificationRequired, receipts)
	got := assessCaseVerification(c, receipts)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy case assessment changed: got=%+v want=%+v", got, want)
	}
}

func TestAssessVerificationOutcomes(t *testing.T) {
	verifier := func(claim string, surface domain.Surface, status domain.VerificationStatus) domain.VerificationRecord {
		return domain.VerificationRecord{
			Claim: claim, Surface: surface, Tool: domain.SurfaceVerifier(surface),
			Purpose: domain.VerificationPurposeVerifierRun, Status: status,
		}
	}
	named := func(claim string, surface domain.Surface, status domain.VerificationStatus) domain.VerificationRecord {
		return domain.VerificationRecord{
			Claim: claim, Surface: surface, Tool: domain.SurfaceVerifier(surface),
			Purpose: domain.VerificationPurposeNamedClaim, Status: status,
		}
	}
	codePass := verifier("structural review of the diff", domain.SurfaceCode, domain.VerifyPassed)

	tests := []struct {
		name     string
		required []string
		receipts []domain.VerificationRecord
		want     VerificationOutcome
	}{
		{
			name: "all required and named claims passed", required: []string{"codemap_review"},
			receipts: []domain.VerificationRecord{codePass, named("the diff is sound", domain.SurfaceCode, domain.VerifyPassed)},
			want:     VerificationVerified,
		},
		{
			name: "a failed named claim defeats another pass", required: []string{"codemap_review"},
			receipts: []domain.VerificationRecord{codePass, named("the redirect works", domain.SurfaceBrowser, domain.VerifyFailed)},
			want:     VerificationFailed,
		},
		{
			name: "a not-run named claim makes a passing task partial", required: []string{"codemap_review"},
			receipts: []domain.VerificationRecord{codePass, named("the redirect works", domain.SurfaceBrowser, domain.VerifyNotRun)},
			want:     VerificationPartial,
		},
		{
			name:     "an unmet required verifier makes a passing task partial",
			required: []string{"codemap_review", "cairntrace_flow"}, receipts: []domain.VerificationRecord{codePass},
			want: VerificationPartial,
		},
		{
			name: "only blocked verification is unverified", required: []string{"codemap_review"},
			receipts: []domain.VerificationRecord{verifier("structural review of the diff", domain.SurfaceCode, domain.VerifyBlocked)},
			want:     VerificationUnverified,
		},
		{
			name: "a failed verifier run is failed", required: []string{"codemap_review", "cairntrace_flow"},
			receipts: []domain.VerificationRecord{codePass, verifier("browser flow specs/login.yml", domain.SurfaceBrowser, domain.VerifyFailed)},
			want:     VerificationFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := assessVerification(test.required, test.receipts)
			if got.Outcome != test.want {
				t.Fatalf("outcome = %q, want %q (assessment=%+v)", got.Outcome, test.want, got)
			}
		})
	}
}

func TestAssessVerificationUsesLatestNamedClaimReceipt(t *testing.T) {
	receipts := []domain.VerificationRecord{
		{Claim: "structural review of the diff", Surface: domain.SurfaceCode, Tool: "codemap", Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyPassed},
		{Claim: "the diff is sound", Surface: domain.SurfaceCode, Tool: "codemap", Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyFailed},
		{Claim: "the diff is sound", Surface: domain.SurfaceCode, Tool: "codemap", Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyPassed},
	}
	assessment := assessVerification([]string{"codemap_review"}, receipts)
	if assessment.Outcome != VerificationVerified {
		t.Fatalf("latest passing claim should supersede its earlier failure: %+v", assessment)
	}
}

func TestAssessVerificationDoesNotCountNamedClaimAsRequiredVerifier(t *testing.T) {
	receipts := []domain.VerificationRecord{{
		Claim: "the diff is sound", Surface: domain.SurfaceCode, Tool: "codemap",
		Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyPassed,
	}}
	assessment := assessVerification([]string{"codemap_review"}, receipts)
	if assessment.Outcome != VerificationPartial || len(assessment.MissingRequired) != 1 {
		t.Fatalf("named claim must not satisfy the verifier-run requirement: %+v", assessment)
	}
}

func TestAssessVerificationMatchesExactRequirementWhenPresent(t *testing.T) {
	receipts := []domain.VerificationRecord{
		{Claim: "structural review", Surface: domain.SurfaceCode, Tool: "codemap", Requirement: "codemap_review", Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyPassed},
		{Claim: "unit tests", Surface: domain.SurfaceCode, Tool: "command", Requirement: "command:unit", Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyFailed},
	}
	assessment := assessVerification([]string{"codemap_review", "command:unit"}, receipts)
	if assessment.Outcome != VerificationFailed || len(assessment.MissingRequired) != 1 || assessment.MissingRequired[0] != "command:unit" {
		t.Fatalf("exact requirements were not preserved: %+v", assessment)
	}
}

func TestAssessVerificationLatestUnboundBatchMasksOlderPass(t *testing.T) {
	receipts := []domain.VerificationRecord{
		{ID: "vr_old", BatchID: "vb_old", Claim: "structural review", Surface: domain.SurfaceCode,
			Tool: "codemap", Requirement: "codemap_review", Purpose: domain.VerificationPurposeVerifierRun,
			Status: domain.VerifyPassed, Binding: domain.VerificationBound},
		{ID: "vr_new", BatchID: "vb_new", Claim: "structural review", Surface: domain.SurfaceCode,
			Tool: "codemap", Requirement: "codemap_review", Purpose: domain.VerificationPurposeVerifierRun,
			Status: domain.VerifyPassed, Binding: domain.VerificationUnbound},
	}
	assessment := assessVerification([]string{"codemap_review"}, receipts)
	if assessment.Outcome == VerificationVerified || len(assessment.MissingRequired) != 1 {
		t.Fatalf("latest unbound batch resurrected older proof: %+v", assessment)
	}
}

func TestAssessVerificationDoesNotTreatUnboundFailureAsDefinitive(t *testing.T) {
	receipts := []domain.VerificationRecord{{
		ID: "vr_unbound", BatchID: "vb_unbound", Claim: "browser flow", Surface: domain.SurfaceBrowser,
		Tool: "cairntrace", Requirement: "cairntrace_flow", Purpose: domain.VerificationPurposeVerifierRun,
		Status: domain.VerifyFailed, Binding: domain.VerificationUnbound,
	}}
	assessment := assessVerification([]string{"cairntrace_flow"}, receipts)
	if assessment.Outcome != VerificationUnverified || len(assessment.FailedClaims) != 0 {
		t.Fatalf("unbound failure should be inconclusive, not definitive: %+v", assessment)
	}
}

func TestAssessVerificationCarriesRequiredNamedClaimsAcrossSameRevisionBatches(t *testing.T) {
	state := func(record domain.VerificationRecord, batch string) domain.VerificationRecord {
		record.BatchID = batch
		record.Revision = "commit-a"
		record.DirtyDigest = "dirty-a"
		record.Binding = domain.VerificationBound
		return record
	}
	receipts := []domain.VerificationRecord{
		state(domain.VerificationRecord{
			ID: "vr_run_old", Claim: "structural review", Surface: domain.SurfaceCode, Tool: "codemap",
			Requirement: "codemap_review", Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyPassed,
		}, "vb_old"),
		state(domain.VerificationRecord{
			ID: "vr_claim_old", ClaimID: "claim_login", Claim: "login works", Surface: domain.SurfaceCode, Tool: "codemap",
			Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyFailed,
		}, "vb_old"),
		state(domain.VerificationRecord{
			ID: "vr_run_new", Claim: "structural review", Surface: domain.SurfaceCode, Tool: "codemap",
			Requirement: "codemap_review", Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyPassed,
		}, "vb_new"),
	}

	assessment := assessVerification([]string{"codemap_review"}, receipts)
	if assessment.Outcome != VerificationFailed || len(assessment.FailedClaims) != 1 {
		t.Fatalf("omitted required claim disappeared from same-state rerun: %+v", assessment)
	}

	receipts = append(receipts, state(domain.VerificationRecord{
		ID: "vr_claim_fixed", ClaimID: "claim_login", Claim: "login works", Surface: domain.SurfaceCode, Tool: "codemap",
		Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyPassed,
	}, "vb_new"))
	assessment = assessVerification([]string{"codemap_review"}, receipts)
	if assessment.Outcome != VerificationVerified {
		t.Fatalf("rerun of the stable claim id did not supersede its failure: %+v", assessment)
	}
}

func TestAssessVerificationDoesNotCarryNamedClaimsAcrossWorkspaceStates(t *testing.T) {
	receipts := []domain.VerificationRecord{
		{ID: "vr_old_claim", BatchID: "vb_old", ClaimID: "claim_old", Claim: "old assertion", Surface: domain.SurfaceCode,
			Tool: "codemap", Purpose: domain.VerificationPurposeNamedClaim, Status: domain.VerifyFailed,
			Revision: "commit-a", DirtyDigest: "dirty-a", Binding: domain.VerificationBound},
		{ID: "vr_new_run", BatchID: "vb_new", Claim: "structural review", Surface: domain.SurfaceCode,
			Tool: "codemap", Requirement: "codemap_review", Purpose: domain.VerificationPurposeVerifierRun, Status: domain.VerifyPassed,
			Revision: "commit-b", DirtyDigest: "dirty-b", Binding: domain.VerificationBound},
	}
	assessment := assessVerification([]string{"codemap_review"}, receipts)
	if assessment.Outcome != VerificationVerified || len(assessment.FailedClaims) != 0 {
		t.Fatalf("claim from a different workspace state was carried forward: %+v", assessment)
	}
}
