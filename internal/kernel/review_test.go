package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// reviewRepo builds a repo on `main` with a divergent `feature` branch checked
// out carrying one changed file, returning the workspace dir.
func reviewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t.co")
	git("config", "user.name", "t")
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "callback.go"), []byte("package src\nfunc HandleCallback() string { return \"/\" }\n"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-q", "-b", "feature")
	_ = os.WriteFile(filepath.Join(dir, "src", "callback.go"), []byte("package src\nfunc HandleCallback() string { return returnTo }\nvar returnTo string\n"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "change")
	return dir
}

func TestReviewBranchApproves(t *testing.T) {
	ws := reviewRepo(t)
	// A passing structural review → the code surface verifies → APPROVE.
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed; blast radius covered", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)
	env, err := k.Review(context.Background(), ReviewInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Phase != domain.PhaseComplete {
		t.Fatalf("review should complete, got ok=%v phase=%s err=%s", env.OK, env.Phase, env.Error)
	}
	if !strings.Contains(env.Summary, "APPROVE") {
		t.Errorf("a change with a passing review should APPROVE, got: %s", env.Summary)
	}
	if !env.Degraded || !hasWarning(env.Warnings, "degraded") {
		t.Fatalf("review hid degraded discovery state: degraded=%t warnings=%v", env.Degraded, env.Warnings)
	}
	// The review is a real, inspectable case with a base ref and evidence.
	c, _ := k.Store().Load(env.TaskID)
	if c.Mode != domain.ModeReview || c.Workspace.BaseRef == "" {
		t.Errorf("expected a ModeReview case with a base ref, got mode=%s base=%q", c.Mode, c.Workspace.BaseRef)
	}
	m, _ := k.TaskMetrics(env.TaskID)
	if m.EvidenceItems == 0 {
		t.Error("a review should leave an evidence trail")
	}
}

func TestReviewExplicitHeadRestoresOriginalBranch(t *testing.T) {
	ws := reviewRepo(t)
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("checkout", "-q", "main")

	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)
	env, err := k.Review(context.Background(), ReviewInput{Base: "main", Head: "feature"})
	if err != nil || !env.OK {
		t.Fatalf("explicit-head review failed: %+v (%v)", env, err)
	}
	if branch := runGit("branch", "--show-current"); branch != "main" {
		t.Fatalf("review left workspace on %q, want original branch main", branch)
	}
}

func TestReviewNoChanges(t *testing.T) {
	// HEAD == default branch, nothing diverged → nothing to review.
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, _ := k.Review(context.Background(), ReviewInput{})
	if !strings.Contains(env.Summary, "no changes to review") {
		t.Errorf("expected 'no changes to review', got: %s", env.Summary)
	}
}

func TestReviewVerdict(t *testing.T) {
	codePass := domain.VerificationRecord{Surface: domain.SurfaceCode, Status: domain.VerifyPassed}
	codeFail := domain.VerificationRecord{Surface: domain.SurfaceCode, Status: domain.VerifyFailed}
	browserNotRun := domain.VerificationRecord{Surface: domain.SurfaceBrowser, Status: domain.VerifyNotRun}
	cases := []struct {
		name     string
		recs     []domain.VerificationRecord
		required []string
		want     string
		full     bool
	}{
		{"all required passed", []domain.VerificationRecord{codePass}, []string{"codemap_review"}, "APPROVE", true},
		{"a failure", []domain.VerificationRecord{codePass, codeFail}, []string{"codemap_review"}, "REQUEST CHANGES", false},
		// Regression (the review's own bug): code passed but the REQUIRED browser
		// verifier never ran → must NOT approve, even though a receipt passed.
		{"required verifier unmet", []domain.VerificationRecord{codePass, browserNotRun}, []string{"codemap_review", "cairntrace_flow"}, "NEEDS VERIFICATION", false},
		// And even with no browser receipt at all, an unmet requirement blocks APPROVE.
		{"required verifier absent", []domain.VerificationRecord{codePass}, []string{"codemap_review", "cairntrace_flow"}, "NEEDS VERIFICATION", false},
	}
	for _, tc := range cases {
		v, full := reviewVerdict(tc.recs, tc.required)
		if v != tc.want || full != tc.full {
			t.Errorf("%s: reviewVerdict = %q/%v, want %q/%v", tc.name, v, full, tc.want, tc.full)
		}
	}
}

func TestHasDefinitiveVerification(t *testing.T) {
	// hasDefinitiveVerification means a verifier *ran* (pass or fail) — used by
	// Review for "verification not possible". Completion itself requires a pass
	// (or accept_failed / unverified); failed-only does not complete bare.
	cases := []struct {
		name string
		recs []domain.VerificationRecord
		want bool
	}{
		{"passed", []domain.VerificationRecord{{Status: domain.VerifyPassed}}, true},
		{"failed counts as real", []domain.VerificationRecord{{Status: domain.VerifyFailed}}, true},
		{"inconclusive only", []domain.VerificationRecord{{Status: domain.VerifyInconclusive}}, false},
		{"not_run only", []domain.VerificationRecord{{Status: domain.VerifyNotRun}}, false},
		{"blocked only", []domain.VerificationRecord{{Status: domain.VerifyBlocked}}, false},
		{"none", nil, false},
		{"mixed inconclusive+failed", []domain.VerificationRecord{{Status: domain.VerifyInconclusive}, {Status: domain.VerifyFailed}}, true},
	}
	for _, tc := range cases {
		if got := hasDefinitiveVerification(tc.recs); got != tc.want {
			t.Errorf("%s: hasDefinitiveVerification = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !hasPassingVerification([]domain.VerificationRecord{{Status: domain.VerifyPassed}}) {
		t.Error("passed should be hasPassing")
	}
	if hasPassingVerification([]domain.VerificationRecord{{Status: domain.VerifyFailed}}) {
		t.Error("failed alone is not a pass")
	}
	if !hasFailedVerification([]domain.VerificationRecord{{Status: domain.VerifyFailed}}) {
		t.Error("failed should be hasFailed")
	}
}

func TestReviewBrowserSurfaceNeverFalseApproves(t *testing.T) {
	// Regression (found by the review's own adversarial review): with a browser
	// surface declared, a passing CODE review plus a user --claim must NOT yield
	// APPROVE while the required browser verifier never ran. The
	// custom claim must augment, not replace, the browser safety-net claim.
	ws := reviewRepo(t)
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "reviewed", Confidence: "high"}}}}
	// No cairntrace adapter → no browser spec can be selected/run.
	k := newTestKernel(t, ws, codemap)
	env, err := k.Review(context.Background(), ReviewInput{
		Surfaces: []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser},
		Claims:   []string{"the diff looks correct"}, // a code-routed custom claim
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env.Summary, "APPROVE") {
		t.Errorf("must not APPROVE when the required browser verifier never ran, got: %s", env.Summary)
	}
	if !strings.Contains(env.Summary, "NEEDS VERIFICATION") {
		t.Errorf("expected NEEDS VERIFICATION, got: %s", env.Summary)
	}
}
