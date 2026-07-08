package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// fakeAdapter returns canned results, letting the kernel be tested without real
// downstream tools (SPEC §23.2 uses fixtures/fakes).
type fakeAdapter struct {
	name   string
	caps   []adapters.Capability
	result adapters.Result
	byOp   map[string]adapters.Result // optional per-operation results
	err    error
	down   bool
}

func (f *fakeAdapter) Name() string                        { return f.name }
func (f *fakeAdapter) Capabilities() []adapters.Capability { return f.caps }
func (f *fakeAdapter) Health(context.Context) error {
	if f.down {
		return adapters.ErrToolMissing
	}
	return nil
}
func (f *fakeAdapter) Execute(_ context.Context, req adapters.Request) (adapters.Result, error) {
	if f.err != nil {
		return adapters.Result{}, f.err
	}
	r := f.result
	if op, ok := f.byOp[req.Operation]; ok {
		r = op
	}
	r.Tool = f.name
	return r, nil
}

// testRepo creates a temp git repo with one committed file and returns its path.
func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.co")
	run("config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

// newTestKernel wires a kernel with a real git adapter plus fakes.
func newTestKernel(t *testing.T, ws string, extra ...adapters.Adapter) *Kernel {
	t.Helper()
	cfg := config.For(ws)
	store, err := casefs.New(cfg.CasesDir)
	if err != nil {
		t.Fatal(err)
	}
	ensureStateIgnored(cfg.CasesDir)
	all := append([]adapters.Adapter{adapters.NewGit()}, extra...)
	return NewWith(cfg, store, adapters.NewRegistry(all...))
}

func TestStartTask(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, err := k.StartTask(context.Background(), StartInput{Goal: "fix redirect", Surfaces: []domain.Surface{domain.SurfaceCode}})
	if err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Phase != domain.PhaseInvestigating {
		t.Fatalf("expected investigating, got %s (ok=%v)", env.Phase, env.OK)
	}
	if env.TaskID == "" {
		t.Fatal("no task id returned")
	}
	// The case must have git identity from orientation.
	c, err := k.Store().Load(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace.Branch == "" || c.Workspace.CommitBefore == "" {
		t.Errorf("orientation did not capture git identity: %+v", c.Workspace)
	}
}

func TestStartRequiresGoal(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{})
	if env.OK {
		t.Error("start without a goal should fail")
	}
}

func TestPlanRejectsNoDisproof(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID

	// No disproof path → rejected, phase stays investigating.
	rej, _ := k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h"}}, Uncertainty: "u", ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}})
	if rej.OK {
		t.Error("plan with no disproof path should be rejected")
	}
	c, _ := k.Store().Load(id)
	if c.Status != domain.PhaseInvestigating {
		t.Errorf("rejected plan should leave phase investigating, got %s", c.Status)
	}
}

func TestPlanRejectsNoBoundaryForChange(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Mode: domain.ModeChange})
	res, _ := k.Plan(PlanInput{TaskID: env.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	if res.OK {
		t.Error("a change task with no boundary should be rejected")
	}
}

func TestPlanAccepts(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	res, _ := k.Plan(PlanInput{
		TaskID:         env.TaskID,
		Hypotheses:     []HypothesisInput{{Statement: "returnTo dropped", DisproveBy: "run browser flow"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}, Symbols: []string{"HandleCallback"}},
		Uncertainty:    "unsure about signing",
	})
	if !res.OK || res.Phase != domain.PhasePlanned {
		t.Fatalf("valid plan should be accepted and move to planned, got ok=%v phase=%s", res.OK, res.Phase)
	}
	if len(res.Hypotheses) != 1 {
		t.Errorf("expected 1 hypothesis in envelope, got %d", len(res.Hypotheses))
	}
}

func TestFullLifecycleCompletes(t *testing.T) {
	ws := testRepo(t)
	// codemap fake returns an authoritative review so a code claim can pass.
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "review ok",
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)

	env, _ := k.StartTask(context.Background(), StartInput{Goal: "fix redirect", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID

	if _, err := k.Investigate(context.Background(), InvestigateInput{TaskID: id, Question: "HandleCallback"}); err != nil {
		t.Fatal(err)
	}
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "returnTo dropped", DisproveBy: "review the diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "unsure"})
	if !plan.OK {
		t.Fatalf("plan failed: %s", plan.Error)
	}

	// Make an in-boundary edit so verify has a diff to review.
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vr, _ := k.Verify(context.Background(), VerifyInput{TaskID: id, Claims: []string{"the diff is structurally sound"}})
	if !vr.OK || vr.Phase != domain.PhaseVerifying {
		t.Fatalf("verify failed: ok=%v phase=%s err=%s", vr.OK, vr.Phase, vr.Error)
	}

	rem, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "fixed returnTo"})
	if !rem.OK || rem.Phase != domain.PhaseComplete {
		t.Fatalf("remember failed: ok=%v phase=%s err=%s", rem.OK, rem.Phase, rem.Error)
	}
	// The hypothesis was never resolved, so completion must nudge about the
	// dangling active hypothesis rather than leave the ledger silently unresolved
	// (dogfooding 2026-07-07).
	if !hasWarning(rem.Warnings, "left unresolved at completion") {
		t.Errorf("expected a dangling-hypothesis nudge; warnings=%v", rem.Warnings)
	}
	// summary.md must exist.
	if _, err := os.Stat(filepath.Join(k.Store().Root(), id, "summary.md")); err != nil {
		t.Errorf("summary not written: %v", err)
	}
}

func TestSummaryRedactsSecrets(t *testing.T) {
	// Review 2026-07-07: summary.md is a durable sink built from raw model/human
	// text (goal, outcome, hypothesis) that never passed the redactor on the way
	// in. A secret in the outcome must be masked at this write boundary, exactly
	// like the sibling vecgrep memory.
	secret := "sk_" + "live_" + "ABCDEF0123456789abcdef"
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "rotate the key", Mode: domain.ModeInvestigate})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id})
	ok, _ := k.Remember(context.Background(), RememberInput{
		TaskID: id, Outcome: "removed " + secret + " from config", VerificationNotPossible: true})
	if !ok.OK {
		t.Fatalf("remember failed: %s", ok.Error)
	}
	data, err := os.ReadFile(filepath.Join(k.Store().Root(), id, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Errorf("summary.md leaked a secret in cleartext:\n%s", data)
	}
	if !strings.Contains(string(data), "«redacted»") {
		t.Errorf("summary.md should carry the redaction mask, got:\n%s", data)
	}
}

func TestRememberRequiresVerification(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Mode: domain.ModeInvestigate})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	// Move to verifying with no claims/receipts.
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id})

	// No receipts and not acknowledged → cannot complete.
	res, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})
	if res.OK {
		t.Error("completion without a verification receipt should be rejected")
	}
	// Explicit acknowledgment → allowed.
	ok, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done", VerificationNotPossible: true})
	if !ok.OK || ok.Phase != domain.PhaseComplete {
		t.Errorf("explicit unverified completion should succeed: %s", ok.Error)
	}
}

func TestScopeDriftDetected(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})

	// Edit an out-of-boundary file.
	if err := os.WriteFile(filepath.Join(ws, "src", "other.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ := k.Store().Load(id)
	sr := k.detectScopeDrift(context.Background(), c, []string{"src/other.go"})
	if sr.Scope != "drift_detected" {
		t.Errorf("expected drift_detected, got %s", sr.Scope)
	}
	// An in-boundary change is clean.
	clean := k.detectScopeDrift(context.Background(), c, []string{"src/callback.go"})
	if clean.Scope != "within_boundary" {
		t.Errorf("expected within_boundary, got %s", clean.Scope)
	}
}

func TestAbortPreservesEvidence(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	before, _ := k.Store().Evidence(id)

	res, _ := k.AbortTask(id, "blocked on a missing credential")
	if !res.OK || res.Phase != domain.PhaseAbandoned {
		t.Fatalf("abort failed: %s", res.Error)
	}
	after, _ := k.Store().Evidence(id)
	if len(after) != len(before) {
		t.Error("abort must not delete evidence")
	}
}

func TestInvestigationBudget(t *testing.T) {
	ws := testRepo(t)
	// A vecgrep fake so investigate has a tool to route to (returns nothing).
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "no hits"}}
	k := newTestKernel(t, ws, vg)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID

	// The default budget is 3 rounds. The 4th should warn about the budget.
	var last domain.Envelope
	for i := 0; i < 4; i++ {
		last, _ = k.Investigate(context.Background(), InvestigateInput{TaskID: id, Question: "something vague to search"})
	}
	c, _ := k.Store().Load(id)
	if c.InvestigationRounds != 4 {
		t.Errorf("expected 4 rounds recorded, got %d", c.InvestigationRounds)
	}
	overBudget := false
	for _, w := range last.Warnings {
		if strings.Contains(w, "exceeds the budget") {
			overBudget = true
		}
	}
	if !overBudget {
		t.Errorf("4th investigation should warn about exceeding the budget; warnings=%v", last.Warnings)
	}
	// Status surfaces the rounds and budget.
	rep, _ := k.Status(context.Background(), id, "standard")
	if rep.InvestigationRounds != 4 || rep.InvestigationBudget != 3 {
		t.Errorf("status should report rounds 4/3, got %d/%d", rep.InvestigationRounds, rep.InvestigationBudget)
	}
}

func TestEvidenceRedactionAtBoundary(t *testing.T) {
	// SPEC §6.3 #4: no secret value may enter an evidence record. A human/model
	// reason routed through Resolve must be redacted at the write boundary.
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	hypID := plan.Hypotheses[0].ID

	secret := "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a"
	_, _ = k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "rejected",
		Reason: "the token " + secret + " was leaked in the log"})

	ev, _ := k.Store().Evidence(id)
	for _, e := range ev {
		if strings.Contains(e.Claim, secret) {
			t.Fatalf("secret leaked into evidence claim: %q", e.Claim)
		}
	}
	// The resolution evidence should be flagged sensitive.
	sawSensitive := false
	for _, e := range ev {
		if e.Kind == domain.KindHumanReport && e.Sensitivity == domain.SensitivitySensitive {
			sawSensitive = true
		}
	}
	if !sawSensitive {
		t.Error("evidence containing a redacted secret should be marked sensitive")
	}
}

func TestVerifyPassCountIgnoresClaimText(t *testing.T) {
	// SPEC §14.2: a claim whose free text contains the word "passed" must NOT be
	// counted as verified when its verifier did not run.
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Surfaces: []domain.Surface{domain.SurfaceBrowser}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	// A browser claim (surface=browser) whose text contains "passed", with NO
	// browser spec supplied → not_run, must not be counted.
	res, _ := k.Verify(context.Background(), VerifyInput{TaskID: id,
		Claims: []string{"the login page passed the token to the handler"}})
	if !strings.Contains(res.Summary, "0/1 claims verified") {
		t.Errorf("a not_run claim mentioning 'passed' must not be counted; summary=%q", res.Summary)
	}
}

func TestVerifyStashesFailedRun(t *testing.T) {
	// SPEC §12.6 / §25 #6: a failed behavioral run is archived to fcheap and the
	// receipt links the durable stash. Needs the real fcheap binary.
	if _, err := exec.LookPath("fcheap"); err != nil {
		t.Skip("fcheap not on PATH")
	}
	ws := testRepo(t)
	// A run bundle directory to stash.
	bundle := filepath.Join(t.TempDir(), "runbundle")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "final.txt"), []byte("terminal left in wrong state"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fake glyphrun returns a FAILED run pointing at the real bundle dir.
	glyph := &fakeAdapter{name: "glyphrun", caps: []adapters.Capability{adapters.CapabilityTerminal},
		result: adapters.Result{Status: adapters.StatusAuthoritative,
			Warnings:  []string{"glyphrun verification did NOT pass (exit 1)"},
			Artifacts: []adapters.ArtifactRef{{Kind: "run_bundle", URI: bundle}}}}
	k := newTestKernel(t, ws, glyph, adapters.NewFcheap())

	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Surfaces: []domain.Surface{domain.SurfaceTerminal}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id, TerminalSpec: "flows/x.yml",
		Claims: []string{"the terminal ends in the right state"}})

	recs, _ := k.Store().Verifications(id)
	linked := false
	for _, r := range recs {
		if r.Surface == domain.SurfaceTerminal && strings.HasPrefix(r.Artifact, "fcheap://stash/") {
			linked = true
		}
	}
	if !linked {
		t.Errorf("failed terminal run should be stashed to fcheap and linked on the receipt; receipts=%+v", recs)
	}
}

type fixedApprover struct{ ok bool }

func (a fixedApprover) Approve(taskID, tool, op string, class domain.ActionClass) bool { return a.ok }

func TestActionGateBlocksExternal(t *testing.T) {
	// SPEC §16.2 #4: external mutation is blocked without approval, allowed with.
	dep := &fakeAdapter{name: "deployer", result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "deployed"}}
	k := newTestKernel(t, testRepo(t), dep)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})

	res := k.run(context.Background(), "deployer", adapters.Request{TaskID: env.TaskID, Operation: "deploy"})
	if res.Status != adapters.StatusBlocked {
		t.Errorf("external mutation should be blocked by default, got %s", res.Status)
	}
	k.SetApprover(fixedApprover{ok: true})
	res = k.run(context.Background(), "deployer", adapters.Request{TaskID: env.TaskID, Operation: "deploy"})
	if res.Status != adapters.StatusAuthoritative {
		t.Errorf("an approved external mutation should run, got %s", res.Status)
	}
}

func TestAuditRecordsActionClass(t *testing.T) {
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, testRepo(t), vg)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.Investigate(context.Background(), InvestigateInput{TaskID: env.TaskID, Question: "some vague thing"})
	data, err := os.ReadFile(filepath.Join(k.Store().Root(), env.TaskID, "commands.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"actionClass":"read_only"`) {
		t.Errorf("audit trail should record the action class; got:\n%s", data)
	}
}

func TestReceiptSensitivity(t *testing.T) {
	// SPEC §16.2 #5: a receipt linked to sensitive evidence is labeled sensitive.
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	ev, err := k.stampEvidence(id, "tvault", adapters.Fact{Kind: "code_location", Claim: "secret key names available", Confidence: "high", Sensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.writeReceipt(id, receiptSpec{Claim: "claim", Surface: domain.SurfaceCode, Tool: "codemap", Status: domain.VerifyPassed, Evidence: []string{ev.ID}}); err != nil {
		t.Fatalf("writeReceipt: %v", err)
	}
	recs, _ := k.Store().Verifications(id)
	if len(recs) == 0 || !recs[len(recs)-1].Sensitive {
		t.Errorf("receipt linked to sensitive evidence should be flagged sensitive; recs=%+v", recs)
	}
}

func TestVideoRefHelpers(t *testing.T) {
	// A path is a bundle; a bare/prefixed id is a stash.
	if videoBundle("/tmp/bug_artifacts/bundle") == "" {
		t.Error("a path should be a bundle")
	}
	if videoBundle("~/Downloads/bug.mp4") == "" {
		t.Error("a media path should be a bundle")
	}
	if videoStash("vt_20260706_bug") != "vt_20260706_bug" {
		t.Error("a bare id should be a stash")
	}
	if videoStash("vidtrace://vt_123") != "vt_123" {
		t.Error("vidtrace:// prefix should be stripped to the stash id")
	}
	if videoStash("/tmp/bundle/dir") != "" {
		t.Error("a path is not a stash")
	}
}

func TestInvestigateWithVideoRunsVidtrace(t *testing.T) {
	// SPEC §19.4: an explicit bug-video bundle triggers a vidtrace investigation.
	vt := &fakeAdapter{name: "vidtrace", caps: []adapters.Capability{adapters.CapabilityArtifacts},
		result: adapters.Result{Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "artifact", Claim: "video failure likely owned by src/checkout.ts:42", Confidence: "low"}}}}
	k := newTestKernel(t, testRepo(t), vt)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	inv, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: "the checkout button does nothing", Video: "/tmp/bug_artifacts/bundle",
	})
	found := false
	for _, f := range inv.Facts {
		if f.Source == "vidtrace" {
			found = true
		}
	}
	if !found {
		t.Errorf("investigate with a video should record vidtrace evidence; facts=%+v", inv.Facts)
	}
}

func TestInvestigateRejectsRawVideoFile(t *testing.T) {
	// Dogfooding 2026-07-07: a raw .mp4 handed to cortex_investigate used to be
	// passed straight through to vidtrace, which failed with an opaque "bundle
	// validation failed: 0/1 checks passed" and no indication a raw file (not a
	// bundle) was the problem. It should now fail fast with clear guidance,
	// without ever calling vidtrace.
	vt := &fakeAdapter{name: "vidtrace", caps: []adapters.Capability{adapters.CapabilityArtifacts},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, testRepo(t), vt)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	res, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: "the checkout button does nothing", Video: "/Users/me/Downloads/bug.mp4",
	})
	if res.OK {
		t.Error("a raw video file should be rejected, not investigated")
	}
	if !strings.Contains(res.Error, "vidtrace extract") {
		t.Errorf("error should point at `vidtrace extract`, got: %s", res.Error)
	}
}

func TestInvestigateSetsDegradedOnPartialResult(t *testing.T) {
	// A tool step that returns anything less than authoritative (e.g. vecgrep's
	// index broken) must surface as a top-level Degraded flag, not just a
	// warning string buried in an array a caller has to notice (dogfooding
	// 2026-07-07).
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusPartial,
			Warnings: []string{"vecgrep: Error: search failed: embedding profile is missing for an existing index"}}}
	k := newTestKernel(t, testRepo(t), vg)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	res, _ := k.Investigate(context.Background(), InvestigateInput{TaskID: env.TaskID, Question: "some vague thing"})
	if !res.Degraded {
		t.Error("investigate should report Degraded when a step returned a non-authoritative result")
	}
	if !res.OK {
		t.Error("a degraded step should still be a soft signal, not a hard failure")
	}
}

func TestRouteStepsCapsCandidates(t *testing.T) {
	// SPEC §7.3 max_candidate_files_returned: discovery searches carry the cap.
	steps := routeSteps(domain.Route{First: "vecgrep", FollowUp: "codemap"}, "some vague behavioral query", nil, 5)
	sawSearch := false
	for _, s := range steps {
		if s.op == "search" {
			sawSearch = true
			if s.input["limit"] != 5 {
				t.Errorf("search step should carry candidate limit 5, got %v", s.input["limit"])
			}
		}
		if s.op == "find" && s.input["top"] != 5 {
			t.Errorf("find step should carry top 5, got %v", s.input["top"])
		}
	}
	if !sawSearch {
		t.Error("expected a discovery search step")
	}
}

func TestMemoryLineFormat(t *testing.T) {
	// SPEC §15.3 memory item format.
	c := &domain.CaseFile{
		ID: "task_X", Goal: "fix post-login redirect",
		Workspace:      domain.Workspace{Repository: "liftclub", CommitBefore: "7e1f4d2"},
		Surfaces:       []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser},
		ChangeBoundary: domain.ChangeBoundary{Symbols: []string{"HandleCallback"}},
	}
	recs := []domain.VerificationRecord{{Artifact: "fcheap://stash/fc_019"}}
	m := memoryLine(c, "returnTo dropped; fixed", recs, "high")
	for _, want := range []string{"repo=liftclub", "symbol=HandleCallback", "behavior=fix post-login redirect",
		"finding=returnTo dropped", "evidence=case task_X", "fcheap://stash/fc_019", "commit=7e1f4d2", "confidence=high"} {
		if !strings.Contains(m, want) {
			t.Errorf("memory line missing %q\n got: %s", want, m)
		}
	}
	// An unverified completion must NOT be recorded as high confidence (SPEC §8.6).
	if mm := memoryLine(c, "guessed", recs, memoryConfidence(false)); !strings.Contains(mm, "confidence=medium") {
		t.Errorf("unverified memory should be medium confidence, got: %s", mm)
	}
}

func TestRawPersistenceAndRetrieval(t *testing.T) {
	// A tool call's raw output is stored once, every fact from it points at the
	// stored blob, and it can be retrieved on demand — redacted (SPEC §10.4).
	ws := testRepo(t)
	secret := "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a"
	vg := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "semantic_search", Claim: "candidate in auth.go", Confidence: "low"}},
			Raw:   `[{"file":"auth.go","token":"` + secret + `"}]`}}
	k := newTestKernel(t, ws, vg)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID

	inv, _ := k.Investigate(context.Background(), InvestigateInput{TaskID: id, Question: "where does auth happen vaguely"})
	if len(inv.Facts) == 0 {
		t.Fatal("expected at least one evidence fact")
	}
	ev, err := k.ReadEvidence(id, inv.Facts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev.RawRef, "/raw/") {
		t.Fatalf("evidence rawRef should point at a stored raw blob, got %q", ev.RawRef)
	}
	// Retrieve the raw — and confirm the secret was redacted before storage.
	raw, err := k.ReadArtifact(id, ev.RawRef)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) {
		t.Errorf("stored raw must be redacted; secret leaked: %q", raw)
	}
	if !strings.Contains(raw, "auth.go") {
		t.Errorf("stored raw should retain non-secret content, got %q", raw)
	}
}

func TestReadArtifactEdgeCases(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	if _, err := k.ReadArtifact("task_x", "fcheap://stash/rb_1"); err == nil {
		t.Error("fcheap ref should return retrieval guidance, not content")
	}
	if _, err := k.ReadArtifact("task_x", "case://task_x/evidence/ev_1"); err == nil {
		t.Error("a self-referencing evidence ref has no stored raw")
	}
	if _, err := k.ReadArtifact("task_x", "garbage"); err == nil {
		t.Error("an unrecognized ref should error")
	}
}

func TestAnnotateBehaviorGating(t *testing.T) {
	// SPEC §12.2: annotate only definitive outcomes, and only when the change
	// boundary names an owning symbol (reasonable-confidence identification).
	k := newTestKernel(t, testRepo(t), adapters.NewCodemap())
	c := &domain.CaseFile{ID: "task_x", Workspace: domain.Workspace{Repository: "x"}}

	// No boundary symbols → no annotation attempted.
	if w := k.annotateBehavior(context.Background(), c, "glyphrun", "s.yml", domain.VerifyPassed, ""); w != nil {
		t.Errorf("no boundary symbols should skip annotation, got %v", w)
	}
	// Inconclusive/errored → nothing to teach, no annotation.
	c.ChangeBoundary.Symbols = []string{"HandleCallback"}
	if w := k.annotateBehavior(context.Background(), c, "glyphrun", "s.yml", domain.VerifyInconclusive, ""); w != nil {
		t.Errorf("inconclusive run should not annotate, got %v", w)
	}
}

func TestVerifyRiskEscalation(t *testing.T) {
	// SPEC §13.3: a high-risk change with no passing structural review warns.
	// No codemap adapter registered → review is blocked (not passed).
	ws := testRepo(t)
	k := newTestKernel(t, ws) // git only; codemap absent → review blocked
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Risk: "high"})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	// Make a diff so review is attempted.
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := k.Verify(context.Background(), VerifyInput{TaskID: id})
	if !hasWarning(res.Warnings, "high-risk change requires a structural diff review") {
		t.Errorf("expected a §13.3 risk-escalation warning; warnings=%v", res.Warnings)
	}
}

func TestVerifyNoDiffWarning(t *testing.T) {
	// SPEC §6.2: a change task with no diff warns before verifying.
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Risk: "low"})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	// No edits → no changed files.
	res, _ := k.Verify(context.Background(), VerifyInput{TaskID: id})
	if !hasWarning(res.Warnings, "no diff/change record detected") {
		t.Errorf("expected a §6.2 no-diff warning; warnings=%v", res.Warnings)
	}
}

func hasWarning(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestBehavioralStatusThreeWay(t *testing.T) {
	pass := adapters.Result{Status: adapters.StatusAuthoritative}
	if got := behavioralStatus(pass); got != domain.VerifyPassed {
		t.Errorf("clean run should be passed, got %s", got)
	}
	fail := adapters.Result{Status: adapters.StatusAuthoritative, Warnings: []string{"glyphrun verification did NOT pass (exit 1)"}}
	if got := behavioralStatus(fail); got != domain.VerifyFailed {
		t.Errorf("behavioral failure should be failed, got %s", got)
	}
	errored := adapters.Result{Status: adapters.StatusAuthoritative, Warnings: []string{"glyphrun run ERRORED (ambiguous — infrastructure/spec error; exit 2)"}}
	if got := behavioralStatus(errored); got != domain.VerifyInconclusive {
		t.Errorf("errored run must be inconclusive, NOT failed, got %s", got)
	}
	unavail := adapters.Result{Status: adapters.StatusUnavailable}
	if got := behavioralStatus(unavail); got != domain.VerifyBlocked {
		t.Errorf("unavailable verifier should be blocked, got %s", got)
	}
	// The structured verdict is authoritative and wins over warning text, so the
	// classification no longer rides on substring drift (review 2026-07-07). Here
	// the verdict says failed even though no "did NOT pass" warning is present.
	structuredFail := adapters.Result{Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictFailed}
	if got := behavioralStatus(structuredFail); got != domain.VerifyFailed {
		t.Errorf("a structured failed verdict should be failed, got %s", got)
	}
	structuredErr := adapters.Result{Status: adapters.StatusAuthoritative, Verdict: adapters.VerdictErrored}
	if got := behavioralStatus(structuredErr); got != domain.VerifyInconclusive {
		t.Errorf("a structured errored verdict should be inconclusive, got %s", got)
	}
}

func TestHighRiskReviewIsNotACleanPass(t *testing.T) {
	// Review 2026-07-07: a codemap review that RAN authoritatively but that
	// codemap rated HIGH risk must not be recorded as a clean structural pass —
	// "the review ran" is not "the diff passed review". It downgrades to
	// inconclusive, so the task cannot then complete on that receipt alone.
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "reviewed",
			Warnings: []string{"diff risk: high (0.91) — hotspot, untested"},
			Facts:    []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "risky change", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id})
	recs, _ := k.Store().Verifications(id)
	var codeReview *domain.VerificationRecord
	for i := range recs {
		if recs[i].Surface == domain.SurfaceCode {
			codeReview = &recs[i]
		}
	}
	if codeReview == nil {
		t.Fatal("expected a code-surface review receipt")
	}
	if codeReview.Status != domain.VerifyInconclusive {
		t.Errorf("a high-risk review should be inconclusive, not %s", codeReview.Status)
	}
	// And with only that inconclusive receipt, completion must be rejected.
	res, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "shipped it"})
	if res.OK {
		t.Error("a task with only an inconclusive review must not complete without acknowledgment")
	}
}

func TestCompletionRejectsInconclusiveOnly(t *testing.T) {
	// Review 2026-07-07: an inconclusive receipt (e.g. an unindexed codemap
	// review) proves nothing about the outcome, so it must NOT satisfy the
	// completion gate on its own — only a definitive passed/failed verdict does.
	ws := testRepo(t)
	// codemap fake returns StatusPartial → the review maps to inconclusive.
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusPartial, Summary: "not indexed"}}
	k := newTestKernel(t, ws, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id})
	res, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})
	if res.OK {
		t.Error("completion with only an inconclusive receipt should be rejected")
	}
	// Explicit acknowledgment still lets it through.
	ok, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done", VerificationNotPossible: true})
	if !ok.OK {
		t.Errorf("explicit unverified completion should succeed: %s", ok.Error)
	}
}

func TestCompletionRejectsNotRunOnly(t *testing.T) {
	// Regression: a task whose only receipt is not_run must NOT complete without
	// an explicit unverified acknowledgment (SPEC §6.3 #2).
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Mode: domain.ModeInvestigate, Surfaces: []domain.Surface{domain.SurfaceBrowser}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	// A browser claim with no browser spec → one not_run receipt.
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id, Claims: []string{"the page loads correctly"}})
	recs, _ := k.Store().Verifications(id)
	if len(recs) == 0 {
		t.Fatal("expected a not_run receipt to exist")
	}
	// Completion must be rejected — the receipt is only not_run.
	res, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})
	if res.OK {
		t.Error("completion with only a not_run receipt should be rejected")
	}
	// Explicit acknowledgment lets it through, with the unverified warning.
	ok, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done", VerificationNotPossible: true})
	if !ok.OK || !hasWarning(ok.Warnings, "WITHOUT verification") {
		t.Errorf("explicit unverified completion should succeed with a warning: ok=%v warnings=%v", ok.OK, ok.Warnings)
	}
}

func TestCompletionWarnsOnMissingRequiredVerifier(t *testing.T) {
	// Regression: a task can pass one required verifier (code) yet leave another
	// required one (browser) never run, and it used to complete as if fully
	// verified with NO warning. Completion must surface the unmet requirement
	// (SPEC §6.2/§14.2).
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "review ok",
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g",
		Surfaces: []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Verify a code claim only — the browser required verifier never runs.
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id, Claims: []string{"the diff is sound"}})
	rem, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})
	if rem.Phase != domain.PhaseComplete {
		t.Fatalf("task should still complete (warning, not block), got %s (%s)", rem.Phase, rem.Error)
	}
	if !hasWarning(rem.Warnings, "INCOMPLETE verification") {
		t.Errorf("completing with an unmet required verifier must warn; warnings=%v", rem.Warnings)
	}
}

func TestResolveRejectedOnTerminalTask(t *testing.T) {
	// Regression: a completed task's hypotheses/evidence are immutable.
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "review ok",
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID
	plan, _ := k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	hypID := plan.Hypotheses[0].ID
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id, Claims: []string{"the diff is sound"}})
	rem, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})
	if rem.Phase != domain.PhaseComplete {
		t.Fatalf("expected complete, got %s (%s)", rem.Phase, rem.Error)
	}
	// Now resolving a hypothesis on the terminal task must be rejected.
	res, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "rejected", Reason: "post-hoc"})
	if res.OK {
		t.Error("resolving a hypothesis on a completed task should be rejected")
	}
}

func TestResolveHypothesis(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "returnTo dropped", DisproveBy: "run browser flow"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "unsure"})
	hypID := plan.Hypotheses[0].ID

	// Reject the hypothesis with a reason.
	res, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "rejected", Reason: "browser flow returned to checkout"})
	if !res.OK {
		t.Fatalf("resolve failed: %s", res.Error)
	}
	// The hypothesis status must be persisted as rejected.
	hyps, _ := k.Store().Hypotheses(id)
	if hyps[0].Status != domain.HypRejected {
		t.Errorf("expected rejected, got %s", hyps[0].Status)
	}
	// A resolution evidence record must be appended (history retained).
	ev, _ := k.Store().Evidence(id)
	found := false
	for _, e := range ev {
		if e.Kind == domain.KindHumanReport {
			found = true
		}
	}
	if !found {
		t.Error("resolution should append a human_report evidence record")
	}
}

func TestResolveValidates(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	// Bad status.
	if res, _ := k.Resolve(ResolveInput{TaskID: env.TaskID, HypothesisID: "hyp_x", Status: "maybe", Reason: "r"}); res.OK {
		t.Error("invalid status should be rejected")
	}
	// Missing reason.
	if res, _ := k.Resolve(ResolveInput{TaskID: env.TaskID, HypothesisID: "hyp_x", Status: "confirmed"}); res.OK {
		t.Error("missing reason should be rejected")
	}
	// Unknown hypothesis.
	if res, _ := k.Resolve(ResolveInput{TaskID: env.TaskID, HypothesisID: "hyp_nope", Status: "confirmed", Reason: "r"}); res.OK {
		t.Error("unknown hypothesis should be rejected")
	}
}

func TestAbortRequiresReason(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	res, _ := k.AbortTask(env.TaskID, "")
	if res.OK {
		t.Error("abort without a reason should be rejected")
	}
}

func TestEntrypointsRejectMissingTask(t *testing.T) {
	// Every entrypoint that loads a task must surface an error (ok:false) for an
	// unknown id rather than panicking or silently succeeding.
	k := newTestKernel(t, testRepo(t))
	ctx := context.Background()
	const bad = "task_DOESNOTEXIST"

	if e, _ := k.Investigate(ctx, InvestigateInput{TaskID: bad, Question: "q"}); e.OK {
		t.Error("investigate on a missing task should not be ok")
	}
	if e, _ := k.Plan(PlanInput{TaskID: bad, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"}); e.OK {
		t.Error("plan on a missing task should not be ok")
	}
	if e, _ := k.Verify(ctx, VerifyInput{TaskID: bad}); e.OK {
		t.Error("verify on a missing task should not be ok")
	}
	if e, _ := k.Remember(ctx, RememberInput{TaskID: bad, Outcome: "x"}); e.OK {
		t.Error("remember on a missing task should not be ok")
	}
	if e, _ := k.Resolve(ResolveInput{TaskID: bad, HypothesisID: "h", Status: "confirmed", Reason: "r"}); e.OK {
		t.Error("resolve on a missing task should not be ok")
	}
	if e, _ := k.Status(ctx, bad, "standard"); e.OK {
		t.Error("status on a missing task should not be ok")
	}
	if e, _ := k.AbortTask(bad, "reason"); e.OK {
		t.Error("abort on a missing task should not be ok")
	}
	if _, err := k.ReadEvidence(bad, "ev_x"); err == nil {
		t.Error("read-evidence on a missing task should error")
	}
	if _, err := k.ReadArtifact(bad, "case://x/raw/y"); err == nil {
		t.Error("read-artifact on a missing raw ref should error")
	}
}

func TestClaimSurfaceRouting(t *testing.T) {
	// Regression: terminal-first + spaced tokens, so "ui"/"tui"/"cli" substrings
	// don't misroute (bug: "the tui..." → browser, "build" → browser).
	cases := map[string]domain.Surface{
		"the tui returns to the shell cleanly": domain.SurfaceTerminal,
		"the cli exits 0":                      domain.SurfaceTerminal,
		"the command writes to stdout":         domain.SurfaceTerminal,
		"the login page redirects correctly":   domain.SurfaceBrowser,
		"the click handler fires":              domain.SurfaceBrowser,
		"the ui updates on submit":             domain.SurfaceBrowser,
		"the build succeeds":                   domain.SurfaceCode,
		"the parser requires balanced braces":  domain.SurfaceCode,
	}
	for claim, want := range cases {
		if got := claimSurface(claim); got != want {
			t.Errorf("claimSurface(%q) = %s, want %s", claim, got, want)
		}
	}
}

func TestNormalizeRisk(t *testing.T) {
	cases := map[string]string{"HIGH": "high", " Low ": "low", "medium": "medium", "": "medium", "bogus": "medium", "MeDiUm": "medium"}
	for in, want := range cases {
		if got := normalizeRisk(in); got != want {
			t.Errorf("normalizeRisk(%q) = %q, want %q", in, got, want)
		}
	}
	// A high-risk task started with a non-canonical value still triggers §13.3.
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Risk: "HIGH"})
	c, _ := k.Store().Load(env.TaskID)
	if c.Risk != "high" {
		t.Fatalf("risk not normalized on start: %q", c.Risk)
	}
	_, _ = k.Plan(PlanInput{TaskID: env.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _=1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := k.Verify(context.Background(), VerifyInput{TaskID: env.TaskID})
	if !hasWarning(res.Warnings, "high-risk change requires") {
		t.Errorf("a --risk HIGH task should trigger §13.3 escalation; warnings=%v", res.Warnings)
	}
}

func TestVideoStashURINotDoubleScheme(t *testing.T) {
	// Regression: a vidtrace:// ref must be treated as a stash, not a bundle,
	// so it doesn't get the scheme prepended twice.
	if videoBundle("vidtrace://vt_123") != "" {
		t.Error("a vidtrace:// ref must not be classified as a bundle")
	}
	if videoStash("vidtrace://vt_123") != "vt_123" {
		t.Error("a vidtrace:// ref should yield the bare stash id")
	}
}

func TestResolveConfirmRequiresEvidence(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	id := env.TaskID
	plan, _ := k.Plan(PlanInput{TaskID: id,
		Hypotheses:     []HypothesisInput{{Statement: "returnTo dropped", DisproveBy: "run browser flow"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}},
		Uncertainty:    "u"})
	hypID := plan.Hypotheses[0].ID

	// Confirming with no cited evidence ID succeeds by auto-minting an evidence
	// record from the reason text — a hypothesis is never promoted on assertion
	// alone, but the caller isn't forced to already have a formal evidence ID
	// either (dogfooding 2026-07-07: an ad hoc repro script has no evidence ID).
	res, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "confirmed", Reason: "looks right"})
	if !res.OK {
		t.Fatalf("confirming with a reason but no evidence id should auto-mint evidence, got error: %s", res.Error)
	}
	h, _ := k.Store().Hypotheses(id)
	if h[0].Status != domain.HypConfirmed || h[0].Confidence != domain.ConfidenceHigh {
		t.Errorf("hypothesis should be confirmed@high, got %s@%s", h[0].Status, h[0].Confidence)
	}
	if len(h[0].Supports) != 1 {
		t.Errorf("auto-confirmed hypothesis should link the auto-minted evidence, got %v", h[0].Supports)
	}

	// Reset back to active so the remaining sub-tests can re-confirm.
	h[0].Status = domain.HypActive
	h[0].Confidence = domain.ConfidenceLow
	h[0].Supports = nil
	if err := k.Store().SaveHypotheses(id, h); err != nil {
		t.Fatal(err)
	}

	// Regression: a cited evidence id that doesn't exist is rejected (no phantom
	// provenance).
	if r, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "confirmed", Reason: "r", Evidence: []string{"ev_phantom"}}); r.OK {
		t.Error("citing a non-existent evidence id must be rejected")
	}

	// Confirming with a real cited evidence id succeeds, links it into Supports,
	// and records it in the resolution claim.
	evs, _ := k.Store().Evidence(id)
	if len(evs) == 0 {
		t.Fatal("expected orientation to have stamped at least one evidence record")
	}
	realID := evs[0].ID
	ok, _ := k.Resolve(ResolveInput{TaskID: id, HypothesisID: hypID, Status: "confirmed",
		Reason: "browser flow preserved returnTo", Evidence: []string{realID}})
	if !ok.OK {
		t.Fatalf("confirm with real evidence should succeed: %s", ok.Error)
	}
	h, _ = k.Store().Hypotheses(id)
	if h[0].Status != domain.HypConfirmed || h[0].Confidence != domain.ConfidenceHigh {
		t.Errorf("hypothesis should be confirmed@high, got %s@%s", h[0].Status, h[0].Confidence)
	}
	if len(h[0].Supports) != 1 || h[0].Supports[0] != realID {
		t.Errorf("confirmed hypothesis should link its supporting evidence, got %v", h[0].Supports)
	}
	after, _ := k.Store().Evidence(id)
	cited := false
	for _, e := range after {
		if e.Kind == domain.KindHumanReport && strings.Contains(e.Claim, realID) {
			cited = true
		}
	}
	if !cited {
		t.Error("resolution record should cite the evidence id in its claim")
	}
}

func TestCapRawForStore(t *testing.T) {
	if got := capRawForStore("hello", 0); got != "hello" {
		t.Errorf("cap of 0 must not truncate, got %q", got)
	}
	if got := capRawForStore("hello world", 5); got != "hello\n…(truncated)" {
		t.Errorf("expected truncation with marker, got %q", got)
	}
	if got := capRawForStore("hi", 100); got != "hi" {
		t.Errorf("short input must be unchanged, got %q", got)
	}
}

func TestInvestigateRecallsPriorMemory(t *testing.T) {
	// Regression: durable memory was write-only. Investigate now recalls prior
	// conclusions for the repo, surfacing them as low-confidence orientation.
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative}, // search: nothing
		byOp: map[string]adapters.Result{
			"memory_recall": {Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
				{Kind: "model_inference", Confidence: "low", Claim: "prior memory: returnTo was dropped in HandleCallback (case task_old)"}}},
		}}
	k := newTestKernel(t, testRepo(t), vecgrep)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	inv, err := k.Investigate(context.Background(), InvestigateInput{TaskID: env.TaskID, Question: "where is the login redirect handled"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range inv.Facts {
		if strings.Contains(f.Claim, "prior memory") {
			found = true
		}
	}
	if !found {
		t.Errorf("investigate should surface a recalled prior memory; facts=%v", factClaimList(inv.Facts))
	}
}

func factClaimList(fs []domain.FactView) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Claim)
	}
	return out
}

func TestGatewaySelfCheckDelegates(t *testing.T) {
	// A thin, read-only diagnostic: it must always return a report naming the
	// server, whether or not mcphub is installed (CI has no mcphub → Supported
	// false; dev has it → a real report). It must never panic or touch the store.
	k := newTestKernel(t, testRepo(t))
	rep := k.GatewaySelfCheck(context.Background(), "", false)
	if rep.Server != "cortex" {
		t.Errorf("self-check should default the server name to cortex, got %q", rep.Server)
	}
}

func TestWorseStatus(t *testing.T) {
	cases := []struct{ a, b, want domain.VerificationStatus }{
		{"", domain.VerifyPassed, domain.VerifyPassed},
		{domain.VerifyPassed, domain.VerifyFailed, domain.VerifyFailed}, // a failure is never masked by a pass
		{domain.VerifyPassed, domain.VerifyInconclusive, domain.VerifyInconclusive},
		{domain.VerifyInconclusive, domain.VerifyPassed, domain.VerifyInconclusive},
		{domain.VerifyPassed, domain.VerifyPassed, domain.VerifyPassed},
		{domain.VerifyFailed, domain.VerifyInconclusive, domain.VerifyFailed},
	}
	for _, tc := range cases {
		if got := worseStatus(tc.a, tc.b); got != tc.want {
			t.Errorf("worseStatus(%q,%q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestVerifyAutoSelectNoCoverage(t *testing.T) {
	// A change task with a browser surface and a diff, but no covering spec found,
	// must report the gap honestly (not silently leave the claim unlabeled). A
	// fake cairntrace can't satisfy the typed SelectSpecs seam, so selection is
	// empty — exactly the no-coverage path.
	ws := testRepo(t)
	cairn := &fakeAdapter{name: "cairntrace", caps: []adapters.Capability{adapters.CapabilityBrowser}}
	k := newTestKernel(t, ws, cairn)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g",
		Surfaces: []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := k.Verify(context.Background(), VerifyInput{TaskID: id})
	if !hasWarning(res.Warnings, "no browser spec covers this change") {
		t.Errorf("expected a no-coverage warning for the browser surface, got %v", res.Warnings)
	}

	// With auto-selection disabled, that warning must not fire.
	env2, _ := k.StartTask(context.Background(), StartInput{Goal: "g2", Surfaces: []domain.Surface{domain.SurfaceBrowser}})
	_, _ = k.Plan(PlanInput{TaskID: env2.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	res2, _ := k.Verify(context.Background(), VerifyInput{TaskID: env2.TaskID, DisableAutoSpecs: true})
	if hasWarning(res2.Warnings, "auto-selection found none") {
		t.Errorf("DisableAutoSpecs should suppress auto-selection, got %v", res2.Warnings)
	}
}

func TestTaskAndWorkspaceMetrics(t *testing.T) {
	ws := testRepo(t)
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative, Summary: "review ok",
			Facts: []adapters.Fact{{Kind: "code_graph", Claim: "diff reviewed", Confidence: "high"}}}}
	k := newTestKernel(t, ws, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "fix redirect", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID
	_, _ = k.Investigate(context.Background(), InvestigateInput{TaskID: id, Question: "HandleCallback"})
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	_ = os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644)
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id, Claims: []string{"the diff is sound"}})
	_, _ = k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})

	tm, err := k.TaskMetrics(id)
	if err != nil {
		t.Fatal(err)
	}
	if !tm.Complete {
		t.Error("task should be complete")
	}
	if tm.ToolCalls == 0 || tm.EvidenceItems == 0 {
		t.Errorf("expected recorded tool calls and evidence, got calls=%d evidence=%d", tm.ToolCalls, tm.EvidenceItems)
	}
	if len(tm.ToolContribution) == 0 {
		t.Error("expected per-tool contribution (§18.2)")
	}
	// Aggregate over the workspace.
	wm, per, err := k.WorkspaceMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if wm.Tasks != 1 || wm.Completed != 1 || len(per) != 1 {
		t.Errorf("workspace metrics: tasks=%d completed=%d per=%d", wm.Tasks, wm.Completed, len(per))
	}
	if wm.CompletionRate != 1.0 {
		t.Errorf("completion rate should be 1.0, got %v", wm.CompletionRate)
	}
}

func TestCompletionRejectsBlockedOnlyVerification(t *testing.T) {
	// Regression (found by the eval harness): when the only verifier's tool is
	// unavailable, its review receipt is `blocked` — which proves nothing, like
	// not_run. Completion must be refused without an explicit acknowledgment,
	// never a fabricated "verified enough".
	ws := testRepo(t)
	k := newTestKernel(t, ws) // no codemap adapter → review is blocked
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	_ = os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){ _ = 1 }\n"), 0o644)
	_, _ = k.Verify(context.Background(), VerifyInput{TaskID: id})
	// A blocked-only verification must not satisfy the completion gate.
	blocked, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done"})
	if blocked.OK {
		t.Error("a task whose only verifier was blocked (tool unavailable) must not complete without acknowledgment")
	}
	ok, _ := k.Remember(context.Background(), RememberInput{TaskID: id, Outcome: "done", VerificationNotPossible: true})
	if !ok.OK || !hasWarning(ok.Warnings, "WITHOUT verification") {
		t.Errorf("explicit-unverified completion should succeed with a warning; ok=%v warns=%v", ok.OK, ok.Warnings)
	}
}

func TestMetricsCallsBeforeEvidenceExcludesOrientation(t *testing.T) {
	// Regression: the git orientation record is stamped at t0 (before any tool
	// call), so it must not be treated as "first evidence" — otherwise
	// CallsBeforeEvidence is pinned to 0 for every git workspace. With only
	// orientation evidence (investigation produced none), all tool calls count.
	empty := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative}} // no facts
	emptyCM := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, testRepo(t), empty, emptyCM)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.Investigate(context.Background(), InvestigateInput{TaskID: env.TaskID, Question: "where is the thing"})
	m, err := k.TaskMetrics(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if m.ToolCalls == 0 {
		t.Fatal("expected recorded tool calls from investigation")
	}
	// Only the git orientation evidence exists → the metric is not the git t0
	// record, so every tool call is counted (not pinned to 0).
	if m.CallsBeforeEvidence != m.ToolCalls {
		t.Errorf("with only orientation evidence, all %d calls should count before first investigation evidence, got %d", m.ToolCalls, m.CallsBeforeEvidence)
	}
}

// TestPlanPersistsTimeoutOverrides verifies the per-task timeout override
// (SPEC §17.2) is accepted at plan time and written to the case file.
func TestPlanPersistsTimeoutOverrides(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.Plan(PlanInput{TaskID: env.TaskID,
		Hypotheses:       []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary:   domain.ChangeBoundary{Files: []string{"src/x.go"}},
		Uncertainty:      "u",
		TimeoutOverrides: map[string]string{"codemap": "45s"},
	})
	c, err := k.Store().Load(env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if c.TimeoutOverrides["codemap"] != "45s" {
		t.Errorf("timeout override not persisted on case file: %v", c.TimeoutOverrides)
	}
}

// TestRunAppliesTimeoutOverride verifies that a per-task timeout override
// bounds the context passed to the adapter (SPEC §17.2).
func TestRunAppliesTimeoutOverride(t *testing.T) {
	sawDeadline := false
	probe := &deadlineAdapter{name: "codemap", onExec: func(ctx context.Context) { _, sawDeadline = ctx.Deadline() }}
	k := newTestKernel(t, testRepo(t), probe)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.Plan(PlanInput{TaskID: env.TaskID,
		Hypotheses:       []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary:   domain.ChangeBoundary{Files: []string{"src/x.go"}},
		Uncertainty:      "u",
		TimeoutOverrides: map[string]string{"codemap": "5s"},
	})
	k.run(context.Background(), "codemap", adapters.Request{TaskID: env.TaskID, Operation: "find", Input: map[string]any{}})
	if !sawDeadline {
		t.Error("adapter context had no deadline despite a codemap timeout override")
	}
}

// deadlineAdapter is a fake codemap adapter that records whether the context
// it received carried a deadline (used to verify per-task timeout overrides).
type deadlineAdapter struct {
	name   string
	onExec func(ctx context.Context)
}

func (d *deadlineAdapter) Name() string { return d.name }
func (d *deadlineAdapter) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapabilityStructure}
}
func (d *deadlineAdapter) Health(context.Context) error { return nil }
func (d *deadlineAdapter) Execute(ctx context.Context, _ adapters.Request) (adapters.Result, error) {
	d.onExec(ctx)
	return adapters.Result{Tool: d.name, Status: adapters.StatusAuthoritative}, nil
}
