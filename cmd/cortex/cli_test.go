/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestMain isolates Cortex's global directories for the whole CLI test binary:
// cases now default to $XDG_STATE_HOME/cortex, so without this the tests would
// write into the developer's real home. CORTEX_HOME collapses config+state into
// one throwaway dir that is removed on exit.
func TestMain(m *testing.M) {
	// Clear the per-dir overrides that would beat CORTEX_HOME (a developer who
	// exported CORTEX_CASES_DIR etc. would otherwise have the CLI tests write into
	// their real cortex state).
	for _, k := range []string{"CORTEX_CASES_DIR", "CORTEX_STATE_DIR", "CORTEX_CONFIG_DIR", "CORTEX_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
	base, err := os.MkdirTemp("", "cortex-clitest-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("CORTEX_HOME", base)
	code := m.Run()
	_ = os.RemoveAll(base)
	os.Exit(code)
}

// resetFlags restores every command's flags to a pristine state, because the
// global rootCmd retains values (and appends to StringArray flags) across
// Execute calls in the same test binary.
func resetFlags(cmd *cobra.Command) {
	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	}
	reset(cmd.PersistentFlags())
	reset(cmd.Flags())
	for _, sub := range cmd.Commands() {
		resetFlags(sub)
	}
}

// runCLI executes the root command with args, capturing os.Stdout.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags(rootCmd)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), err
}

func cliRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t.co"}, {"config", "user.name", "t"}} {
		c := exec.Command("git", a...)
		c.Dir = dir
		_ = c.Run()
	}
	_ = os.WriteFile(filepath.Join(dir, "f.go"), []byte("package a\n"), 0o644)
	c := exec.Command("git", "add", "-A")
	c.Dir = dir
	_ = c.Run()
	c = exec.Command("git", "commit", "-qm", "i")
	c.Dir = dir
	_ = c.Run()
	return dir
}

func TestCLIStartAndList(t *testing.T) {
	ws := cliRepo(t)
	out, err := runCLI(t, "-C", ws, "--json", "start", "fix the redirect bug")
	if err != nil {
		t.Fatalf("start errored: %v", err)
	}
	var env map[string]any
	if e := json.Unmarshal([]byte(out), &env); e != nil {
		t.Fatalf("start output not JSON: %s", out)
	}
	if env["ok"] != true || env["phase"] != "investigating" {
		t.Fatalf("unexpected start envelope: %v", env)
	}
	taskID, _ := env["taskId"].(string)
	if taskID == "" {
		t.Fatal("no taskId")
	}

	// list --json should include the new task.
	lout, err := runCLI(t, "-C", ws, "--json", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lout, taskID) {
		t.Errorf("list should include %s, got: %s", taskID, lout)
	}

	// status --json should report the phase.
	sout, _ := runCLI(t, "-C", ws, "--json", "status", taskID)
	if !strings.Contains(sout, "\"phase\": \"investigating\"") {
		t.Errorf("status should show investigating, got: %s", sout)
	}
}

func TestCLIPlanGateRejectsNoDisproof(t *testing.T) {
	ws := cliRepo(t)
	out, _ := runCLI(t, "-C", ws, "--json", "start", "g")
	var env map[string]any
	_ = json.Unmarshal([]byte(out), &env)
	taskID := env["taskId"].(string)

	// A hypothesis with no disproof path → the command should fail.
	_, err := runCLI(t, "-C", ws, "plan", taskID, "--hypothesis", "returnTo dropped", "--file", "f.go", "--uncertainty", "u")
	if err == nil {
		t.Error("plan with no disproof path should return a non-nil error")
	}
}

func TestCLIJSONExitNonZeroOnKernelReject(t *testing.T) {
	// --json must still surface kernel rejections as a non-nil error so agents
	// that only check the process exit code observe failures (review 2026-07-08).
	ws := cliRepo(t)
	out, err := runCLI(t, "-C", ws, "--json", "start", "g")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(out), &env)
	taskID, _ := env["taskId"].(string)

	out, err = runCLI(t, "-C", ws, "--json", "plan", taskID,
		"--hypothesis", "returnTo dropped", "--file", "f.go", "--uncertainty", "u")
	if err == nil {
		t.Fatal("plan --json with no disproof must return an error (non-zero exit)")
	}
	if !strings.Contains(out, `"ok": false`) && !strings.Contains(out, `"ok":false`) {
		t.Errorf("plan --json should still print the envelope with ok:false, got: %s", out)
	}
	// Human-path rejection also errors (pre-existing).
	if _, err := runCLI(t, "-C", ws, "remember", "task_does_not_exist", "x"); err == nil {
		t.Error("remember of missing task should error")
	}
}

func TestCLIStartRequiresGoal(t *testing.T) {
	ws := cliRepo(t)
	if _, err := runCLI(t, "-C", ws, "start"); err == nil {
		t.Error("start with no goal should error (cobra arg validation)")
	}
}

func TestCLIDoctorJSON(t *testing.T) {
	out, err := runCLI(t, "--json", "doctor")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if e := json.Unmarshal([]byte(out), &d); e != nil {
		t.Fatalf("doctor --json not JSON: %s", out)
	}
	if _, ok := d["tools"]; !ok {
		t.Errorf("doctor should report tools, got: %v", d)
	}
	if _, ok := d["sessions"]; !ok {
		t.Errorf("doctor should report a sessions summary, got: %v", d)
	}
}

func TestBuildHypotheses(t *testing.T) {
	// Inline "statement :: disproof" split.
	hs, err := buildHypotheses([]string{"returnTo dropped :: run the browser flow"}, nil, "low")
	if err != nil || len(hs) != 1 || hs[0].Statement != "returnTo dropped" || hs[0].DisproveBy != "run the browser flow" {
		t.Fatalf("inline split failed: %+v (%v)", hs, err)
	}
	// Paired flags.
	hs, err = buildHypotheses([]string{"h1", "h2"}, []string{"d1", "d2"}, "medium")
	if err != nil || len(hs) != 2 || hs[1].DisproveBy != "d2" {
		t.Fatalf("paired flags failed: %+v (%v)", hs, err)
	}
	// Mismatched counts → error.
	if _, err := buildHypotheses([]string{"h1", "h2"}, []string{"d1"}, "low"); err == nil {
		t.Error("mismatched --hypothesis/--disprove counts should error")
	}
	// No hypotheses → error.
	if _, err := buildHypotheses(nil, nil, "low"); err == nil {
		t.Error("no hypotheses should error")
	}
}

func startTask(t *testing.T, ws string) string {
	t.Helper()
	out, err := runCLI(t, "-C", ws, "--json", "start", "fix redirect")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(out), &env)
	return env["taskId"].(string)
}

func TestCLIFullLifecycle(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)

	// plan (valid, inline disproof)
	if _, err := runCLI(t, "-C", ws, "plan", id,
		"--hypothesis", "returnTo dropped :: review the diff", "--file", "f.go", "--uncertainty", "unsure"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// edit so verify sees a diff
	_ = os.WriteFile(filepath.Join(ws, "f.go"), []byte("package a\nvar X=1\n"), 0o644)
	if _, err := runCLI(t, "-C", ws, "verify", id, "--claim", "the code is sound"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// remember (needs the unverified ack since codemap isn't indexed here)
	rout, err := runCLI(t, "-C", ws, "--json", "remember", id, "returnTo fixed", "--tag", "auth", "--unverified")
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !strings.Contains(rout, "\"phase\": \"complete\"") {
		t.Errorf("remember should complete the task, got: %s", rout)
	}
}

func TestCLIResolveAndAbort(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)
	pout, err := runCLI(t, "-C", ws, "--json", "plan", id,
		"--hypothesis", "h :: d", "--file", "f.go", "--uncertainty", "u")
	if err != nil {
		t.Fatal(err)
	}
	// resolve needs a hypothesis id from the plan envelope.
	var env map[string]any
	_ = json.Unmarshal([]byte(pout), &env)
	hyps, _ := env["hypotheses"].([]any)
	if len(hyps) == 0 {
		t.Fatal("plan returned no hypotheses")
	}
	hypID := hyps[0].(map[string]any)["id"].(string)
	if _, err := runCLI(t, "-C", ws, "resolve", id, hypID, "--status", "rejected", "--reason", "the browser flow passed"); err != nil {
		t.Errorf("resolve: %v", err)
	}

	// abort a fresh task.
	id2 := startTask(t, ws)
	if _, err := runCLI(t, "-C", ws, "abort", id2, "blocked on a credential"); err != nil {
		t.Errorf("abort: %v", err)
	}
}

func TestCLIConfigAndStatusDetail(t *testing.T) {
	ws := cliRepo(t)
	// cortex.yaml override should show up in config.
	_ = os.WriteFile(filepath.Join(ws, "cortex.yaml"), []byte("budget:\n  max_investigation_rounds: 9\n"), 0o644)
	out, err := runCLI(t, "-C", ws, "--json", "config")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"max_investigation_rounds\": 9") {
		t.Errorf("config should reflect the cortex.yaml override, got: %s", out)
	}
	if !strings.Contains(out, "\"sessionsRoot\"") {
		t.Errorf("config should expose the XDG sessions root, got: %s", out)
	}
	// status --detail full includes tool health.
	id := startTask(t, ws)
	sout, _ := runCLI(t, "-C", ws, "--json", "status", id, "--detail", "full")
	if !strings.Contains(sout, "toolHealth") {
		t.Errorf("status --detail full should include tool health, got: %s", sout)
	}
}

func TestTaskIDCompletion(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)

	comps, directive := completeTaskIDs(nil, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected NoFileComp directive, got %v", directive)
	}
	found := false
	for _, c := range comps {
		if strings.HasPrefix(c, id) {
			found = true
		}
	}
	if !found {
		t.Errorf("completion should suggest %s, got %v", id, comps)
	}
	// Once the taskId arg is present, no further completion.
	if c2, _ := completeTaskIDs(nil, []string{id}, ""); c2 != nil {
		t.Errorf("should not complete a second positional arg, got %v", c2)
	}
}

func TestResolveHypCompletion(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)
	// Plan creates a hypothesis whose ID `resolve` should complete.
	if _, err := runCLI(t, "-C", ws, "plan", id,
		"--hypothesis", "cache key ignores query", "--disprove", "inspect key builder",
		"--file", "f.go", "--boundary-reason", "cache module", "--uncertainty", "blast radius"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Second arg (hypId) completes to the task's hypotheses.
	comps, _ := completeResolveArgs(nil, []string{id}, "")
	found := false
	for _, c := range comps {
		if strings.HasPrefix(c, "hyp_") {
			found = true
		}
	}
	if !found {
		t.Errorf("resolve should complete hypothesis IDs, got %v", comps)
	}
	// First arg still completes task IDs.
	if first, _ := completeResolveArgs(nil, []string{}, ""); len(first) == 0 {
		t.Error("resolve first arg should complete task IDs")
	}
}

func TestCLIShow(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)
	out, err := runCLI(t, "show", id)
	if err != nil {
		t.Fatalf("show: %v (%s)", err, out)
	}
	if !strings.Contains(out, "Time in phase") || !strings.Contains(out, "[inv]") {
		t.Errorf("show should include time-in-phase and the loop stepper, got:\n%s", out)
	}
	jout, err := runCLI(t, "--json", "show", id)
	if err != nil {
		t.Fatalf("show --json: %v (%s)", err, jout)
	}
	if !strings.Contains(jout, "\"case\"") {
		t.Errorf("show --json should include the case, got:\n%s", jout)
	}
}

func TestCLIArchiveRoundTrip(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)
	// Terminal first (abort), then archive.
	if _, err := runCLI(t, "-C", ws, "abort", id, "not needed"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if out, err := runCLI(t, "archive", id); err != nil {
		t.Fatalf("archive: %v (%s)", err, out)
	}
	// Shows up under --archived, gone from the default list.
	arch, err := runCLI(t, "--json", "sessions", "--archived")
	if err != nil {
		t.Fatalf("sessions --archived: %v", err)
	}
	if !strings.Contains(arch, id) {
		t.Errorf("archived session %s should appear under --archived, got:\n%s", id, arch)
	}
	// Restore.
	if out, err := runCLI(t, "unarchive", id); err != nil {
		t.Fatalf("unarchive: %v (%s)", err, out)
	}
}

func TestCLIStatusLoopStepper(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws) // lands in investigating
	out, err := runCLI(t, "-C", ws, "status", id)
	if err != nil {
		t.Fatalf("status: %v (%s)", err, out)
	}
	// The reasoning-loop stepper marks the current step; investigating → [inv].
	if !strings.Contains(out, "[inv]") {
		t.Errorf("status should render the loop stepper with the current step, got:\n%s", out)
	}
}

func TestCLIOverview(t *testing.T) {
	ws := cliRepo(t)
	_ = startTask(t, ws)

	out, err := runCLI(t, "--json", "overview")
	if err != nil {
		t.Fatalf("overview --json: %v (%s)", err, out)
	}
	if !strings.Contains(out, "\"sessions\"") {
		t.Errorf("overview --json should include a sessions field, got:\n%s", out)
	}
	hout, err := runCLI(t, "overview")
	if err != nil {
		t.Fatalf("overview: %v (%s)", err, hout)
	}
	if !strings.Contains(hout, "By repo") {
		t.Errorf("human overview should include a By repo section, got:\n%s", hout)
	}
}

func TestCLITimeline(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)

	out, err := runCLI(t, "--json", "timeline", id)
	if err != nil {
		t.Fatalf("timeline --json: %v (%s)", err, out)
	}
	if !strings.Contains(out, "\"kind\": \"phase\"") {
		t.Errorf("timeline --json should include phase events, got:\n%s", out)
	}
	// Human render smoke: the phase rows are labeled.
	hout, err := runCLI(t, "timeline", id)
	if err != nil {
		t.Fatalf("timeline: %v (%s)", err, hout)
	}
	if !strings.Contains(hout, "phase") {
		t.Errorf("human timeline missing phase rows:\n%s", hout)
	}
}

func TestCLISessions(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)

	// The global --json view surfaces the freshly started task across workspaces.
	out, err := runCLI(t, "--json", "sessions")
	if err != nil {
		t.Fatalf("sessions --json: %v (%s)", err, out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("sessions --json should include %s, got:\n%s", id, out)
	}
	// Human view renders the repo slug (filtered to this workspace).
	hout, err := runCLI(t, "sessions", "--repo", filepath.Base(ws))
	if err != nil {
		t.Fatalf("sessions: %v (%s)", err, hout)
	}
	if !strings.Contains(hout, filepath.Base(ws)) {
		t.Errorf("human sessions should include repo %s, got:\n%s", filepath.Base(ws), hout)
	}
	// A freshly started session is not stale.
	sout, err := runCLI(t, "sessions", "--stale")
	if err != nil {
		t.Fatalf("sessions --stale: %v (%s)", err, sout)
	}
	if !strings.Contains(sout, "no stale sessions") {
		t.Errorf("a fresh session should not be flagged stale, got:\n%s", sout)
	}
}

func TestCLIRouteResolvedJSON(t *testing.T) {
	question := "sporadic behavior with no clear owner"
	out, err := runCLI(t, "--json", "route", question)
	if err != nil {
		t.Fatalf("route default: %v (%s)", err, out)
	}
	var got struct {
		Question string   `json:"question"`
		Surfaces []string `json:"surfaces"`
		First    string   `json:"first"`
		FollowUp string   `json:"followUp"`
		Why      string   `json:"why"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("route output not JSON: %v (%s)", err, out)
	}
	want := domain.RouteFor(question, nil)
	if got.Question != question || got.First != want.First || got.FollowUp != want.FollowUp || got.Why != want.Why {
		t.Fatalf("route JSON is not a RouteFor projection: got %+v, want %+v", got, want)
	}
	if got.Surfaces == nil || len(got.Surfaces) != 0 {
		t.Fatalf("default route surfaces = %#v, want empty array", got.Surfaces)
	}
}

func TestCLIRouteRedactsQuestionWithoutChangingDecision(t *testing.T) {
	token := "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a"
	question := "does this deploy have token " + token
	for _, args := range [][]string{{"--json", "route", question}, {"route", question}} {
		out, err := runCLI(t, args...)
		if err != nil {
			t.Fatalf("route secret question: %v (%s)", err, out)
		}
		if strings.Contains(out, token) {
			t.Fatalf("route output leaked the raw question secret: %s", out)
		}
		if !strings.Contains(out, "tvault") {
			t.Fatalf("redaction changed the secret-capability route: %s", out)
		}
	}
}

func TestCLIRouteRepeatableBrowserSurface(t *testing.T) {
	question := "behavior is wrong"
	out, err := runCLI(t, "--json", "route", question, "--surface", "code", "--surface", "browser")
	if err != nil {
		t.Fatalf("route browser: %v (%s)", err, out)
	}
	var got struct {
		Surfaces []string `json:"surfaces"`
		First    string   `json:"first"`
		FollowUp string   `json:"followUp"`
		Why      string   `json:"why"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("route output not JSON: %v (%s)", err, out)
	}
	want := domain.RouteFor(question, []domain.Surface{domain.SurfaceCode, domain.SurfaceBrowser})
	if got.First != want.First || got.FollowUp != want.FollowUp || got.Why != want.Why {
		t.Fatalf("browser route JSON = %+v, want %+v", got, want)
	}
	if strings.Join(got.Surfaces, ",") != "code,browser" {
		t.Fatalf("repeatable surfaces lost or reordered: %v", got.Surfaces)
	}
}

func TestCLIRouteExportsMatrixAndRejectsInvalidSurfaces(t *testing.T) {
	out, err := runCLI(t, "--json", "route")
	if err != nil {
		t.Fatalf("route matrix: %v (%s)", err, out)
	}
	var matrix []domain.RoutingRule
	if err := json.Unmarshal([]byte(out), &matrix); err != nil {
		t.Fatalf("route matrix output not JSON: %v (%s)", err, out)
	}
	if len(matrix) != len(domain.RoutingMatrix) {
		t.Fatalf("route matrix has %d rows, want %d", len(matrix), len(domain.RoutingMatrix))
	}
	for _, surface := range []string{"", "database"} {
		if _, err := runCLI(t, "route", "question", "--surface", surface); err == nil {
			t.Errorf("route accepted invalid surface %q", surface)
		}
	}
}

func TestCLIReindexCasesEmptyJSON(t *testing.T) {
	t.Setenv("CORTEX_HOME", t.TempDir())
	out, err := runCLI(t, "--json", "reindex-cases")
	if err != nil {
		t.Fatalf("reindex-cases: %v (%s)", err, out)
	}
	var got struct {
		SessionsScanned   int      `json:"sessionsScanned"`
		SessionLoadFailed int      `json:"sessionLoadFailed"`
		RecordsScanned    int      `json:"recordsScanned"`
		Indexed           int      `json:"indexed"`
		Skipped           int      `json:"skipped"`
		Failed            int      `json:"failed"`
		Warnings          []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("reindex output not JSON: %v (%s)", err, out)
	}
	if got.SessionsScanned != 0 || got.SessionLoadFailed != 0 || got.RecordsScanned != 0 || got.Indexed != 0 || got.Skipped != 0 || got.Failed != 0 || got.Warnings == nil || len(got.Warnings) != 0 {
		t.Fatalf("empty reindex report = %+v, want all-zero counts and empty warnings", got)
	}
}

func TestCLIRm(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)
	if _, err := runCLI(t, "-C", ws, "abort", id, "not needed"); err != nil {
		t.Fatalf("abort: %v", err)
	}

	// Without --force, rm is a dry run — nothing is removed.
	out, err := runCLI(t, "rm", id)
	if err != nil {
		t.Fatalf("rm (dry run): %v (%s)", err, out)
	}
	if !strings.Contains(out, "would delete") {
		t.Errorf("dry run should say 'would delete', got:\n%s", out)
	}

	// With --force, it's actually deleted.
	out, err = runCLI(t, "rm", id, "--force")
	if err != nil {
		t.Fatalf("rm --force: %v (%s)", err, out)
	}
	if !strings.Contains(out, "permanently deleted") {
		t.Errorf("forced delete should confirm removal, got:\n%s", out)
	}

	// A second rm --force reports the session is gone.
	if _, err := runCLI(t, "rm", id, "--force"); err == nil {
		t.Error("expected an error deleting an already-removed session")
	}
}
