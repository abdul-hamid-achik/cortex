package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestAllowedPathsContractGatesPlanAndVerify(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	if err := os.WriteFile(filepath.Join(ws, "src", "decoy.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, ws)
	started, err := k.StartTask(context.Background(), StartInput{Goal: "fix the callback", Risk: "low",
		AllowedPaths: []string{"src/callback.go", "src/*_test.go"}})
	if err != nil || !started.OK {
		t.Fatalf("start: %+v %v", started, err)
	}

	widened, _ := k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "both files share the bug", DisproveBy: "a test"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go", "src/decoy.go"}}, Uncertainty: "u"})
	if widened.OK || !strings.Contains(widened.Error, "src/decoy.go") || len(widened.Actions) == 0 {
		t.Fatalf("a boundary outside the owner's paths must be rejected with a retry: %+v", widened)
	}
	planned, _ := k.Plan(PlanInput{TaskID: started.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "callback drops the error", DisproveBy: "a test"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if !planned.OK {
		t.Fatalf("an in-contract plan should pass: %+v", planned)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(ws, "src", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("callback.go", "package src\nfunc HandleCallback(){ _ = 51 }\n")
	write("decoy.go", "package src\n// touched\n")
	blocked, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID, DriftAcknowledged: true})
	if blocked.OK || !strings.Contains(blocked.Error, "outside the owner's allowed paths") || !strings.Contains(blocked.Error, "src/decoy.go") {
		t.Fatalf("verify must refuse out-of-contract changes even with drift acknowledged: %+v", blocked)
	}
	status, _ := k.Status(context.Background(), started.TaskID, "standard")
	if status.Scope == nil || len(status.Scope.OutsideAllowedPaths) != 1 || len(status.AllowedPaths) != 2 {
		t.Fatalf("status should show the contract and the violation: %+v", status)
	}

	write("decoy.go", "package src\n")
	write("callback_test.go", "package src\n")
	allowed, _ := k.Verify(context.Background(), VerifyInput{TaskID: started.TaskID})
	if !allowed.OK {
		t.Fatalf("after reverting, in-contract changes (including tests) should verify: %+v", allowed)
	}
}

func TestAllowedPathsAreImmutableCaseIdentity(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "fix", Risk: "low", AllowedPaths: []string{"src/callback.go"}})
	c, err := k.Store().Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	c.AllowedPaths = append(c.AllowedPaths, "src/decoy.go")
	if err := k.Store().Save(c); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("widening allowed paths after creation must be refused: %v", err)
	}
	for _, bad := range [][]string{{"/etc/passwd"}, {"../x"}, {"src/../../x"}, {"src/["}} {
		if got, _ := k.StartTask(context.Background(), StartInput{Goal: "fix", AllowedPaths: bad}); got.OK {
			t.Fatalf("invalid allowed path %v accepted", bad)
		}
	}
	first, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "keyed", IdempotencyKey: "k1", AllowedPaths: []string{"a.go"}}})
	second, _ := k.OpenTask(context.Background(), OpenInput{StartInput: StartInput{Goal: "keyed", IdempotencyKey: "k1", AllowedPaths: []string{"b.go"}}})
	if !first.OK || second.OK || !strings.Contains(second.Error, "allowed paths") {
		t.Fatalf("a keyed retry with a different path contract must be refused: first=%+v second=%+v", first, second)
	}
}

func gitCommitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}
