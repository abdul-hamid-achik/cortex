/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func openSurvey(t *testing.T, ws string) string {
	t.Helper()
	out, err := runCLI(t, "-C", ws, "--json", "open", "survey the repo", "--mode", "survey", "--actor", "agent-cli")
	if err != nil {
		t.Fatalf("open survey: %v\n%s", err, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("open output: %v\n%s", err, out)
	}
	id, _ := env["taskId"].(string)
	if id == "" {
		t.Fatalf("no task id: %s", out)
	}
	return id
}

func TestCLILongRunningCommands(t *testing.T) {
	ws := cliRepo(t)
	id := openSurvey(t, ws)

	out, err := runCLI(t, "-C", ws, "coverage", id)
	if err != nil || !strings.Contains(out, "survey coverage") || !strings.Contains(out, "unseen") {
		t.Fatalf("coverage: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "-C", ws, "investigate", id, "what lives here", "--module", "."); err != nil || !strings.Contains(out, "round 1") {
		t.Fatalf("scoped investigate: %v\n%s", err, out)
	}

	if _, err := runCLI(t, "-C", ws, "finding", "add", id, "x", "--kind", "smell"); err == nil {
		t.Fatal("bad finding kind must fail")
	}
	out, err = runCLI(t, "-C", ws, "--json", "finding", "add", id, "callback ignores errors", "--kind", "bug", "--severity", "high", "--file", "f.go")
	if err != nil {
		t.Fatalf("finding add: %v\n%s", err, out)
	}
	var rep map[string]any
	_ = json.Unmarshal([]byte(out), &rep)
	fid := rep["finding"].(map[string]any)["id"].(string)
	if out, err := runCLI(t, "-C", ws, "finding", "list", id); err != nil || !strings.Contains(out, fid) || !strings.Contains(out, "1 open") {
		t.Fatalf("finding list: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "-C", ws, "finding", "dismiss", id, fid); err == nil {
		t.Fatal("dismiss without a reason must fail")
	}
	if out, err := runCLI(t, "-C", ws, "finding", "triage", id, fid, "--reason", "confirmed"); err != nil || !strings.Contains(out, "triaged") {
		t.Fatalf("triage: %v\n%s", err, out)
	}
	out, err = runCLI(t, "-C", ws, "finding", "convert", id, fid, "--actor", "agent-fix")
	if err != nil || !strings.Contains(out, "child") {
		t.Fatalf("convert: %v\n%s", err, out)
	}

	if _, err := runCLI(t, "-C", ws, "dossier", "add", id, "--title", "t", "--summary", "s"); err == nil {
		t.Fatal("dossier add without a module must fail")
	}
	if out, err := runCLI(t, "-C", ws, "dossier", "add", id, "--module", ".", "--kind", "convention", "--title", "one package", "--summary", "everything lives in package a"); err != nil || !strings.Contains(out, "dossier entry") {
		t.Fatalf("dossier add: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "-C", ws, "dossier", "list"); err != nil || !strings.Contains(out, "one package") {
		t.Fatalf("dossier list: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "-C", ws, "dossier", "refresh"); err != nil || !strings.Contains(out, "refreshed") {
		t.Fatalf("dossier refresh: %v\n%s", err, out)
	}

	if out, err := runCLI(t, "-C", ws, "workplan", "add", id, "do a", "--id", "a", "--mode", "change", "--criterion", "a_done=a is done"); err != nil || !strings.Contains(out, "work item a added") {
		t.Fatalf("workplan add: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "-C", ws, "workplan", "add", id, "do b", "--id", "b", "--after", "a"); err != nil || !strings.Contains(out, "2 items") {
		t.Fatalf("workplan add b: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "-C", ws, "workplan", "list", id); err != nil || !strings.Contains(out, "blocked") {
		t.Fatalf("workplan list: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "-C", ws, "workplan", "next", id); err == nil {
		t.Fatal("next without an actor must fail")
	}
	if out, err := runCLI(t, "-C", ws, "workplan", "next", id, "--actor", "agent-1"); err != nil || !strings.Contains(out, "claimed by agent-1") {
		t.Fatalf("workplan next: %v\n%s", err, out)
	}

	if out, err := runCLI(t, "-C", ws, "job", "list", id); err != nil || !strings.Contains(out, "0 in flight") {
		t.Fatalf("job list: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "-C", ws, "job", "cancel", id, "job_nope"); err == nil {
		t.Fatal("cancel of an unknown job must fail")
	}

	if _, err := runCLI(t, "-C", ws, "resume", id, "--since", "yesterday"); err == nil {
		t.Fatal("bad cursor must fail")
	}
	out, err = runCLI(t, "-C", ws, "resume", id)
	if err != nil || !strings.Contains(out, "Cortex compact handoff") || !strings.Contains(out, "Survey coverage") {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	out, err = runCLI(t, "-C", ws, "status", id)
	if err != nil || !strings.Contains(out, "coverage") || !strings.Contains(out, "findings") || !strings.Contains(out, "workplan") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "-C", ws, "--json", "status", id); err != nil || !strings.Contains(out, `"coverage"`) {
		t.Fatalf("status json: %v\n%s", err, out)
	}
}
