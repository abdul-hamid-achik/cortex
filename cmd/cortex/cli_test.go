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

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

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
	// status --detail full includes tool health.
	id := startTask(t, ws)
	sout, _ := runCLI(t, "-C", ws, "--json", "status", id, "--detail", "full")
	if !strings.Contains(sout, "toolHealth") {
		t.Errorf("status --detail full should include tool health, got: %s", sout)
	}
}
