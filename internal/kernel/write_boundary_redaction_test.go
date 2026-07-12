package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestModelInputsAreRedactedBeforeCaseWrites(t *testing.T) {
	const secret = "ghp_16C7e42F292c6912E7710c838347Ae178B4a99"
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, ws, codemap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "repair " + secret, Risk: "low"})
	if !started.OK {
		t.Fatalf("start = %+v", started)
	}
	c, _ := k.Store().Load(started.TaskID)
	if strings.Contains(c.Goal, secret) || !strings.Contains(c.Goal, "«redacted»") {
		t.Fatalf("goal reached case.json unredacted: %q", c.Goal)
	}
	planned, _ := k.Plan(PlanInput{
		TaskID: started.TaskID,
		Hypotheses: []HypothesisInput{{
			Statement: "token " + secret + " caused it", DisproveBy: "run check with " + secret,
		}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}, Reason: "avoid " + secret},
		Uncertainty:    "unknown " + secret,
	})
	if !planned.OK {
		t.Fatalf("plan = %+v", planned)
	}
	plan, _ := k.Store().LoadPlan(started.TaskID)
	serialized := plan.Hypotheses[0].Statement + plan.Hypotheses[0].DisproveBy.Note + plan.Uncertainty + plan.ChangeBoundary.Reason
	if strings.Contains(serialized, secret) || !strings.Contains(serialized, "«redacted»") {
		t.Fatalf("plan reached case files unredacted: %+v", plan)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 12 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, _ := k.Verify(context.Background(), VerifyInput{
		TaskID: started.TaskID, ClaimSpecs: []domain.VerificationClaim{{
			Statement: "claim includes " + secret, Surface: domain.SurfaceCode, Contract: "codemap_review",
		}},
	})
	if !verified.OK {
		t.Fatalf("verify = %+v", verified)
	}
	receipts, _ := k.Store().Verifications(started.TaskID)
	last := receipts[len(receipts)-1]
	if strings.Contains(last.Claim, secret) || !last.Sensitive {
		t.Fatalf("verification receipt leaked/unlabeled secret: %+v", last)
	}
}

func TestPlanRejectsSecretShapedBoundary(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	result, _ := k.Plan(PlanInput{
		TaskID:         started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"TOKEN=supersecretvalue"}}, Uncertainty: "u",
	})
	if result.OK {
		t.Fatalf("secret-shaped boundary accepted: %+v", result)
	}
}
