package kernel

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// A case whose registered criteria cannot be proven used to have no completion
// path but abort. The escape hatch must repeat the exact missing criterion IDs,
// can never produce a verified outcome, and records what stayed unproven.
func TestRememberCanRecordExactlyTheUnmetCriteria(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{
		Goal: "fix the flaky retry", Risk: "low",
		AcceptanceCriteria: []domain.AcceptanceCriterion{
			{ID: "retry_ok", Statement: "retries stop after three attempts"},
			{ID: "no_commit", Statement: "no commit is made"},
		},
	})
	_, _ = k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "review"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u",
	})
	if err := os.WriteFile(ws+"/src/callback.go", []byte("package src\nfunc HandleCallback(){ _ = 31 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if verified, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID}); !verified.OK {
		t.Fatalf("verify: %+v", verified)
	}

	refused, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "fixed"})
	if refused.OK || !strings.Contains(refused.Error, "accept_missing_criteria") {
		t.Fatalf("the refusal should name the explicit escape hatch: %+v", refused)
	}
	var offered []any
	for _, action := range refused.Actions {
		if ids, ok := action.Arguments["acceptMissingCriteria"].([]string); ok {
			for _, id := range ids {
				offered = append(offered, id)
			}
		}
	}
	if len(offered) != 2 {
		t.Fatalf("the remember action should carry the exact missing ids: %+v", refused.Actions)
	}

	partial, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "fixed",
		CriteriaUnmetAcknowledged: []string{"retry_ok"}})
	if partial.OK {
		t.Fatalf("acknowledging a subset of the missing criteria must not complete: %+v", partial)
	}

	done, _ := k.Remember(context.Background(), RememberInput{TaskID: started.TaskID, Outcome: "fixed",
		CriteriaUnmetAcknowledged: []string{"no_commit", "retry_ok"}})
	if !done.OK {
		t.Fatalf("acknowledging exactly the missing criteria should complete: %+v", done)
	}
	if !strings.Contains(strings.Join(done.Warnings, "\n"), "no_commit") {
		t.Fatalf("completion must warn about every unproven criterion: %v", done.Warnings)
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if status.VerificationOutcome == VerificationVerified {
		t.Fatalf("unmet criteria can never read as verified: %+v", status)
	}
	summary, err := os.ReadFile(summaryPath(k, started.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "Unmet acceptance criteria") || !strings.Contains(string(summary), "retry_ok") {
		t.Fatalf("summary.md must record the unmet criteria:\n%s", summary)
	}
}
