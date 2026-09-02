package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

func vecgrepFake(file string) *fakeAdapter {
	return &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Tool: "vecgrep", Operation: "search", Status: adapters.StatusAuthoritative,
			Facts: []adapters.Fact{{Kind: "semantic_search", Claim: "func HandleCallback handles the callback", Confidence: "medium",
				Location: &adapters.Location{File: file, StartLine: 2, Symbol: "HandleCallback"}}}}}
}

// ---- findings ----

func TestFindingLifecycleConvertsIntoLinkedChild(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "understand the callback", Mode: domain.ModeInvestigate})
	id := env.TaskID
	note, _ := k.RecordObservation(ObservationInput{TaskID: id, Claim: "callback ignores the error return", Location: &domain.Location{File: "src/callback.go"}})
	if !note.OK || len(note.Facts) != 1 {
		t.Fatalf("note failed: %+v", note)
	}
	evID := note.Facts[0].ID

	bad, _ := k.RecordFinding(FindingInput{TaskID: id, Title: "x", Evidence: []string{"ev_nope"}})
	if bad.OK || !strings.Contains(bad.Error, "does not exist") {
		t.Fatalf("unknown evidence should be rejected: %+v", bad.Envelope)
	}
	if bad.Actions == nil || !strings.Contains(strings.Join(bad.Actions[0].Candidates["evidence"], ","), evID) {
		t.Fatalf("rejection should offer real evidence ids: %+v", bad.Actions)
	}
	rep, _ := k.RecordFinding(FindingInput{TaskID: id, Title: "callback swallows errors", Kind: "bug", Severity: "high",
		Evidence: []string{evID}, Files: []string{filepath.Join(ws, "src", "callback.go")}})
	if !rep.OK || rep.Finding == nil || rep.Finding.Status != domain.FindingOpen {
		t.Fatalf("record finding: %+v", rep.Envelope)
	}
	if rep.Finding.Files[0] != "src/callback.go" {
		t.Errorf("files should be workspace-relative, got %v", rep.Finding.Files)
	}
	fid := rep.Finding.ID

	dismiss, _ := k.UpdateFindingStatus(ctx, FindingStatusInput{TaskID: id, FindingID: fid, Status: "dismissed"})
	if dismiss.OK {
		t.Fatal("dismiss without a reason must be rejected")
	}
	conv, _ := k.ConvertFinding(ctx, ConvertFindingInput{TaskID: id, FindingID: fid, Actor: "agent-a"})
	if !conv.OK || conv.ChildTaskID == "" {
		t.Fatalf("convert: %+v", conv.Envelope)
	}
	child, err := k.Store().Load(conv.ChildTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID != id || child.Mode != domain.ModeChange || child.Risk != "high" {
		t.Errorf("child linkage/mode/risk wrong: %+v", child)
	}
	if len(child.AcceptanceCriteria) != 1 || child.AcceptanceCriteria[0].ID != rep.Finding.CriterionID() || child.AcceptanceCriteria[0].Statement != "callback swallows errors" {
		t.Errorf("child should carry the finding as its acceptance criterion: %+v", child.AcceptanceCriteria)
	}
	again, _ := k.ConvertFinding(ctx, ConvertFindingInput{TaskID: id, FindingID: fid, Actor: "agent-a"})
	if !again.OK || again.ChildTaskID != conv.ChildTaskID {
		t.Errorf("convert must be retry-safe: %+v", again.Envelope)
	}
	list, _ := k.ListFindings(id, "")
	if list.Counts.Converted != 1 || list.Counts.Total != 1 {
		t.Errorf("counts = %+v", list.Counts)
	}
	st, _ := k.Status(ctx, id, "standard")
	if st.Findings == nil || st.Findings.Converted != 1 || len(st.ChildTaskIDs) != 1 {
		t.Errorf("status should roll up findings and the child: findings=%+v children=%v", st.Findings, st.ChildTaskIDs)
	}
	second, _ := k.RecordFinding(FindingInput{TaskID: id, Title: "naming is inconsistent", Kind: "improvement"})
	dismissed, _ := k.UpdateFindingStatus(ctx, FindingStatusInput{TaskID: id, FindingID: second.Finding.ID, Status: "dismissed", Reason: "matches the repo convention"})
	if !dismissed.OK || dismissed.Finding.Status != domain.FindingDismissed || dismissed.Finding.Reason == "" {
		t.Errorf("dismiss with reason: %+v", dismissed.Envelope)
	}
	cp, _ := k.Store().ReadCheckpoint(id)
	if !strings.Contains(cp, "## Findings") {
		t.Errorf("checkpoint should carry the backlog:\n%s", cp)
	}
}

// ---- dossier + freshness ----

func TestDossierEntryOrientsNewCasesAndGoesStaleWhenFilesChange(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "understand src", Mode: domain.ModeInvestigate})
	id := env.TaskID
	note, _ := k.RecordObservation(ObservationInput{TaskID: id, Claim: "HandleCallback is the only entry point", Location: &domain.Location{File: "src/callback.go"}})
	evID := note.Facts[0].ID

	missing, _ := k.UpsertDossierEntry(ctx, DossierEntryInput{TaskID: id, Kind: "architecture", Title: "t", Summary: "s"})
	if missing.OK || !strings.Contains(missing.Error, "module") {
		t.Fatalf("module is required: %+v", missing.Envelope)
	}
	rep, _ := k.UpsertDossierEntry(ctx, DossierEntryInput{TaskID: id, Module: "src", Kind: "invariant", Title: "single entry point",
		Summary: "every callback goes through HandleCallback", Files: []string{"src/callback.go"}, Evidence: []string{evID}})
	if !rep.OK || rep.Entry == nil || rep.Entry.Commit == "" {
		t.Fatalf("upsert: %+v", rep.Envelope)
	}
	if _, err := os.Stat(filepath.Join(rep.Path, "dossier.md")); err != nil {
		t.Errorf("dossier.md should be rendered: %v", err)
	}
	evidence, _ := k.Store().Evidence(id)
	last := evidence[len(evidence)-1]
	if last.Source.Tool != "dossier" || last.Kind != domain.KindModelInference || len(last.DerivedFrom) != 1 {
		t.Errorf("the case ledger should record the promotion with provenance: %+v", last)
	}

	// A second case on the same repo is oriented by the dossier.
	second, _ := k.StartTask(ctx, StartInput{Goal: "fix the callback"})
	found := false
	for _, f := range second.Facts {
		if f.Source == "dossier" && strings.Contains(f.Claim, "single entry point") {
			found = true
		}
	}
	if !found {
		t.Errorf("orientation should surface the dossier entry: %+v", second.Facts)
	}

	fresh, _ := k.Dossier(ctx, DossierListInput{})
	if fresh.Stale != 0 || len(fresh.Entries) != 1 {
		t.Fatalf("entry should be fresh: %+v", fresh)
	}
	// Change the file and commit: the entry must go stale.
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback(){}\nfunc Other(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, ws, "add", "-A")
	gitIn(t, ws, "commit", "-qm", "change")
	stale, _ := k.Dossier(ctx, DossierListInput{StaleOnly: true})
	if stale.Stale != 1 || len(stale.Entries) != 1 || !stale.Entries[0].Stale {
		t.Fatalf("entry should be stale after the file changed: %+v", stale)
	}
	refreshed, _ := k.RefreshDossier(ctx)
	if !refreshed.OK || refreshed.Stale != 1 {
		t.Errorf("refresh should persist the stale mark: %+v", refreshed.Envelope)
	}
	third, _ := k.StartTask(ctx, StartInput{Goal: "another"})
	for _, f := range third.Facts {
		if f.Source == "dossier" {
			t.Errorf("stale entries must not orient new cases: %+v", f)
		}
	}
	// The original note's evidence is stale too.
	st, _ := k.Status(ctx, id, "standard")
	if len(st.StaleEvidence) == 0 || st.StaleEvidence[0].ID != evID {
		t.Errorf("status should flag stale evidence: %+v", st.StaleEvidence)
	}
}

func TestVerifyWarnsWhenHypothesisSupportIsStale(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "g", Mode: domain.ModeInvestigate})
	id := env.TaskID
	note, _ := k.RecordObservation(ObservationInput{TaskID: id, Claim: "callback returns nil", Location: &domain.Location{File: "src/callback.go"}})
	evID := note.Facts[0].ID
	if _, err := k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d", Supports: []string{evID}}}, Uncertainty: "u"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "callback.go"), []byte("package src\nfunc HandleCallback() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, _ := k.Verify(ctx, VerifyInput{TaskID: id})
	joined := strings.Join(v.Warnings, "\n")
	if !strings.Contains(joined, "stale") {
		t.Errorf("verify should warn about stale supporting evidence, got: %s", joined)
	}
}

// ---- survey ----

// surveyRepo is testRepo plus two more modules (internal/kernel and root files).
func surveyRepo(t *testing.T) string {
	t.Helper()
	ws := testRepo(t)
	if err := os.MkdirAll(filepath.Join(ws, "internal", "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "internal", "kernel", "k.go"), []byte("package kernel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, ws, "add", "-A")
	gitIn(t, ws, "commit", "-qm", "tree")
	return ws
}

func TestSurveyLedgerDrivesRoundsAndGatesCompletion(t *testing.T) {
	ws := surveyRepo(t)
	vg := vecgrepFake("src/callback.go")
	k := newTestKernel(t, ws, vg)
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "understand the whole repo", Mode: domain.ModeSurvey})
	if !env.OK {
		t.Fatalf("start survey: %+v", env)
	}
	id := env.TaskID
	ledger, err := k.Store().LoadCoverage(id)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Source != "git_tree" || len(ledger.Modules) != 3 {
		t.Fatalf("ledger = %+v", ledger)
	}
	cov, _ := k.Coverage(id, true)
	if cov.Coverage.Unseen != 3 || cov.Coverage.Next == "" {
		t.Fatalf("coverage = %+v", cov.Coverage)
	}
	first := cov.Coverage.Next

	round, _ := k.Investigate(ctx, InvestigateInput{TaskID: id, Question: "what lives here"})
	if !round.OK {
		t.Fatalf("round: %+v", round)
	}
	if !strings.Contains(round.Summary, "["+first+" round 1]") {
		t.Errorf("survey round should default to the next module %q: %s", first, round.Summary)
	}
	treeFact := false
	for _, f := range round.Facts {
		if f.Source == "git" && strings.Contains(f.Claim, "tracked file") {
			treeFact = true
		}
	}
	if !treeFact {
		t.Errorf("survey rounds should anchor on the module tree: %+v", round.Facts)
	}
	reqs := vg.requests()
	if len(reqs) == 0 || (first != "." && reqs[0].Str("scope") != first) {
		t.Errorf("discovery should be scoped to the module dir: %+v", reqs)
	}
	after, _ := k.Coverage(id, true)
	if after.Coverage.Unseen != 2 || after.Coverage.Explored != 1 {
		t.Errorf("coverage after one round = %+v", after.Coverage)
	}
	st, _ := k.Status(ctx, id, "standard")
	if st.Coverage == nil || st.Coverage.Next == first {
		t.Errorf("status coverage should advance: %+v", st.Coverage)
	}
	hasSurveyAction := false
	for _, a := range st.Actions {
		if a.Tool == "cortex_investigate" && a.Arguments["module"] != nil {
			hasSurveyAction = true
		}
	}
	if !hasSurveyAction {
		t.Errorf("status should offer the next module as an action: %+v", st.Actions)
	}

	// A dossier entry promotes the module to summarized.
	dos, _ := k.UpsertDossierEntry(ctx, DossierEntryInput{TaskID: id, Module: first, Title: "t", Summary: "s"})
	if !dos.OK {
		t.Fatalf("dossier: %+v", dos.Envelope)
	}
	summ, _ := k.Coverage(id, true)
	if summ.Coverage.Summarized != 1 {
		t.Errorf("summarized should be 1: %+v", summ.Coverage)
	}

	// Completion refuses partial coverage unless acknowledged.
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	_, _ = k.Verify(ctx, VerifyInput{TaskID: id})
	res, _ := k.Remember(ctx, RememberInput{TaskID: id, Outcome: "done", VerificationNotPossible: true})
	if res.OK || !strings.Contains(res.Error, "coverage") {
		t.Fatalf("partial survey must not complete silently: %+v", res)
	}
	ok, _ := k.Remember(ctx, RememberInput{TaskID: id, Outcome: "done", VerificationNotPossible: true, AcceptPartialCoverage: true})
	if !ok.OK || ok.Phase != domain.PhaseComplete {
		t.Errorf("acknowledged partial survey should complete: %+v", ok)
	}
}

// ---- workplan ----

func TestWorkplanHandsOutReadyItemsInDependencyOrder(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "campaign", Mode: domain.ModeInvestigate})
	id := env.TaskID
	a, _ := k.AddWorkItem(WorkItemInput{TaskID: id, ID: "a", Goal: "refactor parser", Mode: "change"})
	if !a.OK || a.Ready != 1 {
		t.Fatalf("add a: %+v", a.Envelope)
	}
	b, _ := k.AddWorkItem(WorkItemInput{TaskID: id, ID: "b", Goal: "update callers", Mode: "change", DependsOn: []string{"a"}})
	if !b.OK || b.Ready != 1 || b.Pending != 2 {
		t.Fatalf("add b: %+v ready=%d pending=%d", b.Envelope, b.Ready, b.Pending)
	}
	cyc, _ := k.AddWorkItem(WorkItemInput{TaskID: id, ID: "c", Goal: "loop", DependsOn: []string{"d"}})
	if cyc.OK {
		t.Fatal("unknown dependency must be rejected")
	}
	next, _ := k.NextWorkItem(ctx, NextWorkItemInput{TaskID: id, Actor: "agent-1"})
	if !next.OK || next.ChildTask == "" {
		t.Fatalf("next: %+v", next.Envelope)
	}
	child, _ := k.Store().Load(next.ChildTask)
	if child.ParentTaskID != id || child.Actor != "agent-1" || child.Goal != "refactor parser" {
		t.Errorf("child = %+v", child)
	}
	blocked, _ := k.NextWorkItem(ctx, NextWorkItemInput{TaskID: id, Actor: "agent-2"})
	if !blocked.OK || blocked.ChildTask != "" {
		t.Errorf("b is blocked by a; no item should be handed out: %+v", blocked.Envelope)
	}
	list, _ := k.Workplan(id)
	if list.Items[0].Derived != "active" || list.Items[1].Derived != "blocked" {
		t.Errorf("derived states = %s / %s", list.Items[0].Derived, list.Items[1].Derived)
	}
	// Finish a: b becomes ready.
	_, _ = k.Plan(PlanInput{TaskID: child.ID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/callback.go"}}, Uncertainty: "u"})
	_, _ = k.BeginChange(BeginChangeInput{TaskID: child.ID, Actor: "agent-1"})
	_, _ = k.Verify(ctx, VerifyInput{TaskID: child.ID, Actor: "agent-1", NoOpAcknowledged: true})
	done, _ := k.Remember(ctx, RememberInput{TaskID: child.ID, Outcome: "done", VerificationNotPossible: true})
	if !done.OK {
		t.Fatalf("complete child: %+v", done)
	}
	ready, _ := k.NextWorkItem(ctx, NextWorkItemInput{TaskID: id, Actor: "agent-2"})
	if !ready.OK || ready.ChildTask == "" {
		t.Fatalf("b should be ready once a completed: %+v", ready.Envelope)
	}
	st, _ := k.Status(ctx, id, "standard")
	if st.Workplan == nil || st.Workplan.Done != 1 || st.Workplan.Active != 1 {
		t.Errorf("status workplan rollup = %+v", st.Workplan)
	}
}

// ---- resume ----

func TestResumeReturnsCheckpointAndDeltasSinceCursor(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws, vecgrepFake("src/callback.go"))
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "g", Mode: domain.ModeInvestigate})
	id := env.TaskID
	if _, err := k.Investigate(ctx, InvestigateInput{TaskID: id, Question: "callback"}); err != nil {
		t.Fatal(err)
	}
	first, _ := k.Resume(ctx, ResumeInput{TaskID: id})
	if !first.OK || !strings.Contains(first.Checkpoint, "# Cortex compact handoff") || !first.CheckpointFresh {
		t.Fatalf("resume: %+v", first)
	}
	cursor := first.Cursor
	time.Sleep(5 * time.Millisecond)
	k.now = func() time.Time { return cursor.Add(time.Second) }
	_, _ = k.RecordObservation(ObservationInput{TaskID: id, Claim: "later note"})
	delta, _ := k.Resume(ctx, ResumeInput{TaskID: id, Since: cursor})
	if len(delta.Evidence) != 1 || delta.Evidence[0].Claim != "later note" {
		t.Errorf("delta should hold only the later note: %+v", delta.Evidence)
	}
}

// ---- jobs ----

func TestBackgroundJobRunsRoundsAndBlocksCompletionWhileInFlight(t *testing.T) {
	ws := surveyRepo(t)
	k := newTestKernel(t, ws, vecgrepFake("src/callback.go"))
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "understand", Mode: domain.ModeSurvey})
	id := env.TaskID
	var spawned []string
	k.SetJobSpawner(func(workspace, taskID, jobID, logPath string) (int, error) {
		spawned = append(spawned, jobID)
		return os.Getpid(), nil
	})
	rep, _ := k.StartJob(ctx, JobInput{TaskID: id, Fanout: true, Max: 2})
	if !rep.OK || rep.Job == nil || rep.Job.RoundsTotal != 2 || len(spawned) != 1 {
		t.Fatalf("start job: %+v spawned=%v", rep.Envelope, spawned)
	}
	jobID := rep.Job.ID
	dup, _ := k.StartJob(ctx, JobInput{TaskID: id, Question: "another"})
	if dup.OK {
		t.Fatal("a second job while one is in flight must be refused")
	}
	// Run the worker in-process.
	if err := k.RunJob(ctx, id, jobID); err != nil {
		t.Fatalf("run job: %v", err)
	}
	list, _ := k.ListJobs(ctx, id)
	if len(list.Jobs) != 1 || list.Jobs[0].Status != domain.JobDone || list.Jobs[0].RoundsDone != 2 || len(list.Jobs[0].EvidenceIDs) == 0 {
		t.Fatalf("job after run = %+v", list.Jobs)
	}
	cov, _ := k.Coverage(id, true)
	if cov.Coverage.Explored != 2 {
		t.Errorf("fan-out should have explored two modules: %+v", cov.Coverage)
	}
	cp, _ := k.Store().ReadCheckpoint(id)
	if !strings.Contains(cp, "Survey coverage") {
		t.Errorf("checkpoint should be rewritten after the job:\n%s", cp)
	}

	// A dead worker is repaired to failed on read.
	k.SetJobSpawner(func(workspace, taskID, jobID, logPath string) (int, error) { return 2147483000, nil })
	dead, _ := k.StartJob(ctx, JobInput{TaskID: id, Question: "q"})
	if !dead.OK {
		t.Fatalf("start dead job: %+v", dead.Envelope)
	}
	repaired, _ := k.ListJobs(ctx, id)
	var got domain.Job
	for _, j := range repaired.Jobs {
		if j.ID == dead.Job.ID {
			got = j
		}
	}
	if got.Status != domain.JobFailed || !strings.Contains(got.Error, "exited") {
		t.Errorf("dead worker should be failed: %+v", got)
	}

	// Cancel a queued job whose "worker" is us: completion is blocked while it is in flight.
	k.SetJobSpawner(func(workspace, taskID, jobID, logPath string) (int, error) { return os.Getpid(), nil })
	live, _ := k.StartJob(ctx, JobInput{TaskID: id, Question: "q2"})
	_, _ = k.Plan(PlanInput{TaskID: id, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}}, Uncertainty: "u"})
	_, _ = k.Verify(ctx, VerifyInput{TaskID: id})
	blocked, _ := k.Remember(ctx, RememberInput{TaskID: id, Outcome: "o", VerificationNotPossible: true, AcceptPartialCoverage: true})
	if blocked.OK || !strings.Contains(blocked.Error, "background job") {
		t.Fatalf("remember must refuse while a job is in flight: %+v", blocked)
	}
	canceled, _ := k.CancelJob(ctx, id, live.Job.ID)
	if !canceled.OK || canceled.Job.Status != domain.JobCanceled {
		t.Fatalf("cancel: %+v", canceled.Envelope)
	}
	ok, _ := k.Remember(ctx, RememberInput{TaskID: id, Outcome: "o", VerificationNotPossible: true, AcceptPartialCoverage: true})
	if !ok.OK {
		t.Errorf("remember after cancel: %+v", ok)
	}
}

func TestEvidenceCarriesRecordingCommit(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws, vecgrepFake("src/callback.go"))
	ctx := context.Background()
	env, _ := k.StartTask(ctx, StartInput{Goal: "g"})
	_, _ = k.Investigate(ctx, InvestigateInput{TaskID: env.TaskID, Question: "callback"})
	evidence, _ := k.Store().Evidence(env.TaskID)
	for _, ev := range evidence {
		if ev.Source.Tool == "vecgrep" && ev.Commit == "" {
			t.Errorf("discovery evidence should carry the recording commit: %+v", ev)
		}
	}
}
