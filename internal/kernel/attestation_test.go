package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// attestationCase starts a change case with one behavioral criterion proven by
// the unit command and one process criterion, plans, edits, and records a note
// the attestation can cite.
func attestationCase(t *testing.T, unitPasses bool) (*Kernel, string, string, string) {
	t.Helper()
	t.Setenv("CORTEX_APPROVE_COMMANDS", "1")
	ws := testRepo(t)
	verdict := adapters.VerdictPassed
	if !unitPasses {
		verdict = adapters.VerdictFailed
	}
	command := &fakeAdapter{name: "command", result: adapters.Result{Status: adapters.StatusAuthoritative, Verdict: verdict}}
	k := newTestKernel(t, ws, command)
	k.cfg.Verifiers = map[string]config.CommandVerifier{
		"unit": {Argv: []string{"go", "test", "./..."}, Kind: domain.KindUnitTest, Surface: domain.SurfaceCode, Timeout: time.Minute},
	}
	started, err := k.StartTask(context.Background(), StartInput{Goal: "fix the callback without committing", Risk: "low",
		AcceptanceCriteria: []domain.AcceptanceCriterion{
			{ID: "cb_ok", Statement: "the callback keeps the error"},
			{ID: "no_commit", Statement: "no commit is made", Kind: domain.CriterionKindProcess},
		}})
	if err != nil || !started.OK {
		t.Fatalf("start: %+v %v", started, err)
	}
	_, _ = k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a failing test reproduces it"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "callers"})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 41 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	note, err := k.RecordObservation(ObservationInput{TaskID: started.TaskID, Claim: "git log shows HEAD unchanged; nothing was committed", Origin: "agent"})
	if err != nil || !note.OK || len(note.Facts) == 0 {
		t.Fatalf("note: %+v %v", note, err)
	}
	return k, started.TaskID, note.Facts[0].ID, ws
}

func unitClaim() domain.VerificationClaim {
	return domain.VerificationClaim{ID: "cb_ok", Statement: "the callback keeps the error", Surface: domain.SurfaceCode, Verifier: "command:unit", Contract: "unit"}
}

func TestAttestedProcessCriterionCompletesAlongsideVerifierProof(t *testing.T) {
	k, taskID, evidenceID, _ := attestationCase(t, true)
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID,
		ClaimSpecs:   []domain.VerificationClaim{unitClaim()},
		Attestations: []AttestationInput{{ClaimID: "no_commit", Evidence: []string{evidenceID}, Note: "checked git log"}}})
	if !verified.OK {
		t.Fatalf("verify: %+v", verified)
	}
	status, _ := k.Status(context.Background(), taskID, "standard")
	if status.VerificationOutcome != VerificationVerified {
		t.Fatalf("verifier proof plus an attested process criterion should verify: %+v", status)
	}
	if len(status.AttestedCriteria) != 1 || status.AttestedCriteria[0] != "no_commit" || len(status.MissingCriteria) != 0 {
		t.Fatalf("the process criterion should be reported as attested, not proven: %+v", status)
	}
	for _, id := range status.SatisfiedCriteria {
		if id == "no_commit" {
			t.Fatalf("an attested criterion must not be listed as verifier-proven: %+v", status.SatisfiedCriteria)
		}
	}
	done, _ := k.Remember(context.Background(), RememberInput{TaskID: taskID, Outcome: "fixed without committing"})
	if !done.OK {
		t.Fatalf("remember: %+v", done)
	}
	summary, _ := os.ReadFile(summaryPath(k, taskID))
	if !strings.Contains(string(summary), "attested, not verifier-proven") {
		t.Fatalf("summary.md must label attested criteria:\n%s", summary)
	}
}

func TestAttestationNeverSubstitutesForVerifierProof(t *testing.T) {
	k, taskID, evidenceID, _ := attestationCase(t, false)
	verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID,
		ClaimSpecs:   []domain.VerificationClaim{unitClaim()},
		Attestations: []AttestationInput{{ClaimID: "no_commit", Evidence: []string{evidenceID}}}})
	if !verified.OK {
		t.Fatalf("verify: %+v", verified)
	}
	status, _ := k.Status(context.Background(), taskID, "standard")
	if status.VerificationOutcome == VerificationVerified {
		t.Fatalf("a failing unit test cannot be rescued by an attestation: %+v", status)
	}
}

func TestAttestationGates(t *testing.T) {
	k, taskID, evidenceID, _ := attestationCase(t, true)
	cases := []struct {
		name string
		in   AttestationInput
		want string
	}{
		{"behavioral criterion", AttestationInput{ClaimID: "cb_ok", Evidence: []string{evidenceID}}, "not a process criterion"},
		{"unregistered id", AttestationInput{ClaimID: "made_up", Evidence: []string{evidenceID}}, "not a registered acceptance criterion"},
		{"no evidence", AttestationInput{ClaimID: "no_commit"}, "evidence"},
		{"unknown evidence", AttestationInput{ClaimID: "no_commit", Evidence: []string{"ev_missing"}}, "not evidence in task"},
		{"statement mismatch", AttestationInput{ClaimID: "no_commit", Statement: "a different rule", Evidence: []string{evidenceID}}, "registered statement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := k.Verify(context.Background(), VerifyInput{TaskID: taskID, Attestations: []AttestationInput{tc.in}})
			if got.OK || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("want rejection containing %q, got %+v", tc.want, got)
			}
		})
	}
}

func TestAttestationGoesStaleWhenTheWorkspaceChanges(t *testing.T) {
	k, taskID, evidenceID, ws := attestationCase(t, true)
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: taskID,
		ClaimSpecs:   []domain.VerificationClaim{unitClaim()},
		Attestations: []AttestationInput{{ClaimID: "no_commit", Evidence: []string{evidenceID}}}})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: taskID, ClaimSpecs: []domain.VerificationClaim{unitClaim()}})
	status, _ := k.Status(context.Background(), taskID, "standard")
	if len(status.AttestedCriteria) != 0 || len(status.MissingCriteria) != 1 || status.MissingCriteria[0] != "no_commit" {
		t.Fatalf("an attestation about an older workspace state must not carry over: %+v", status)
	}
}

func TestCompleteHandoffCarriesTheAttestation(t *testing.T) {
	k, taskID, evidenceID, ws := attestationCase(t, true)
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: taskID,
		ClaimSpecs:   []domain.VerificationClaim{unitClaim()},
		Attestations: []AttestationInput{{ClaimID: "no_commit", Evidence: []string{evidenceID}}}})
	if done, _ := k.Remember(context.Background(), RememberInput{TaskID: taskID, Outcome: "fixed"}); !done.OK {
		t.Fatalf("remember: %+v", done)
	}
	h, err := BuildHandoffIn(ws, taskID, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, receipt := range h.Receipts {
		found = found || (receipt.Purpose == domain.VerificationPurposeAttestation && receipt.ClaimID == "no_commit")
	}
	if !found || len(h.Verification.AttestedCriteria) != 1 {
		t.Fatalf("the complete handoff must carry the attestation receipt: receipts=%+v verification=%+v", h.Receipts, h.Verification)
	}
}
