/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

func TestCLIAgentWorkflowPreservesContractsAndStructuredActions(t *testing.T) {
	ws := cliRepo(t)
	opened := qaRunCLIEnvelope(t, "--json", "-C", ws, "open", "repair redirect contract",
		"--actor", "agent-a", "--idempotency-key", "qa-cli-run")
	retried := qaRunCLIEnvelope(t, "--json", "-C", ws, "open", "repair redirect contract",
		"--actor", "agent-a", "--idempotency-key", "qa-cli-run")
	if opened.TaskID == "" || retried.TaskID != opened.TaskID {
		t.Fatalf("open retry changed identity: first=%+v retry=%+v", opened, retried)
	}
	qaRequireCLIAction(t, opened, "cortex_investigate")
	taskID := opened.TaskID

	note := qaRunCLIEnvelope(t, "--json", "-C", ws, "note", taskID,
		"redirect behavior is externally visible", "--kind", "constraint", "--origin", "agent", "--actor", "agent-a")
	if !note.OK || len(note.Facts) != 1 || note.Facts[0].Kind != domain.KindHumanReport {
		t.Fatalf("note did not return provenance-bearing evidence: %+v", note)
	}
	qaRequireCLIAction(t, note, "cortex_investigate")

	paused := qaRunCLIEnvelope(t, "--json", "-C", ws, "decision", "request", taskID,
		"--question", "Which rollout should we use?", "--requester", "agent-a",
		"--option", "safe=Two-step|Slower but reversible",
		"--option", "fast=One-step|Faster but harder rollback")
	if paused.Phase != domain.PhaseNeedsHumanDecision || len(paused.Artifacts) != 1 {
		t.Fatalf("decision request did not pause with a durable decision: %+v", paused)
	}
	decisionID := paused.Artifacts[0].ID
	decisionAction := qaRequireCLIAction(t, paused, "cortex_answer_decision")
	if decisionAction.Arguments["decisionId"] != decisionID || !strings.Contains(decisionAction.Command, decisionID) {
		t.Fatalf("decision action is not directly invokable: %+v", decisionAction)
	}
	answered := qaRunCLIEnvelope(t, "--json", "-C", ws, "decision", "answer", taskID, decisionID,
		"--answer", "safe", "--responder", "human-a")
	if answered.Phase != domain.PhaseInvestigating {
		t.Fatalf("decision answer did not resume investigating: %+v", answered)
	}
	qaRequireCLIAction(t, answered, "cortex_investigate")

	planned := qaRunCLIEnvelope(t, "--json", "-C", ws, "plan", taskID,
		"--hypothesis", "callback drops the return path", "--disprove", "review the callback diff",
		"--file", "callback.go", "--uncertainty", "redirect signing may differ")
	if planned.Phase != domain.PhasePlanned {
		t.Fatalf("plan = %+v", planned)
	}
	planAction := qaRequireCLIAction(t, planned, "cortex_begin_change")
	if !qaCLIStringContains(planAction.Inputs, "actor") {
		t.Fatalf("begin-change action omits its required actor input: %+v", planAction)
	}

	begun := qaRunCLIEnvelope(t, "--json", "-C", ws, "begin-change", taskID,
		"--actor", "agent-a", "--ttl", "2m")
	if begun.Phase != domain.PhaseChanging {
		t.Fatalf("begin-change = %+v", begun)
	}
	verifyAction := qaRequireCLIAction(t, begun, "cortex_verify")
	if verifyAction.Arguments["actor"] != "agent-a" || !strings.Contains(verifyAction.Command, "--actor agent-a") {
		t.Fatalf("leased verify action is not bound to its actor: %+v", verifyAction)
	}
	if err := os.WriteFile(filepath.Join(ws, "callback.go"), []byte("package a\nfunc HandleCallback(){ _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withoutActorOut, withoutActorErr := runCLI(t, "--json", "-C", ws, "verify", taskID,
		"--claim", "redirect is preserved", "--claim-surface", "code", "--claim-contract", "codemap_review")
	if withoutActorErr == nil {
		t.Fatalf("leased verify without --actor should fail: %s", withoutActorOut)
	}
	var withoutActor domain.Envelope
	if err := json.Unmarshal([]byte(withoutActorOut), &withoutActor); err != nil {
		t.Fatalf("leased verify rejection is not JSON: %v (%s)", err, withoutActorOut)
	}
	if withoutActor.OK || !strings.Contains(withoutActor.Error, "verify must name that actor") {
		t.Fatalf("unexpected leased verify rejection: %+v", withoutActor)
	}

	verified := qaRunCLIEnvelope(t, "--json", "-C", ws, "verify", taskID,
		"--actor", "agent-a", "--claim", "redirect is preserved", "--claim-surface", "code",
		"--claim-verifier", "codemap", "--claim-contract", "codemap_review")
	if verified.Phase != domain.PhaseVerifying {
		t.Fatalf("typed verify = %+v", verified)
	}
	qaRequireCLIAction(t, verified, "cortex_verify")

	showOut, err := runCLI(t, "--json", "show", taskID)
	if err != nil {
		t.Fatalf("show: %v (%s)", err, showOut)
	}
	var view kernel.SessionView
	if err := json.Unmarshal([]byte(showOut), &view); err != nil {
		t.Fatalf("show output is not a session view: %v (%s)", err, showOut)
	}
	if !qaCLITypedReceipt(view.Receipts, domain.SurfaceCode, "codemap_review") {
		t.Fatalf("CLI typed claim fields did not reach receipts: %+v", view.Receipts)
	}

	handoffOut, err := runCLI(t, "--json", "handoff", taskID)
	if err != nil {
		t.Fatalf("handoff: %v (%s)", err, handoffOut)
	}
	var handoff kernel.Handoff
	if err := json.Unmarshal([]byte(handoffOut), &handoff); err != nil {
		t.Fatalf("handoff output is not JSON: %v (%s)", err, handoffOut)
	}
	if handoff.TaskID != taskID || len(handoff.Evidence) == 0 || len(handoff.Actions) == 0 || len(handoff.Decisions) != 1 {
		t.Fatalf("handoff omitted agent continuation context: %+v", handoff)
	}
	handoffPath := filepath.Join(t.TempDir(), "handoff.md")
	if output, err := runCLI(t, "-C", ws, "handoff", taskID, "-o", handoffPath); err != nil {
		t.Fatalf("handoff file: %v (%s)", err, output)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(handoffPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("handoff permissions = %o, want 600", info.Mode().Perm())
		}
	}

	k, err := kernel.New(config.For(ws))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Store().WriteRaw(taskID, "raw_agent_surface", "0123456789"); err != nil {
		t.Fatal(err)
	}
	previewOut, err := runCLI(t, "--json", "-C", ws, "read-artifact", taskID,
		"case://"+taskID+"/raw/raw_agent_surface", "--max-bytes", "4")
	if err != nil {
		t.Fatalf("read-artifact: %v (%s)", err, previewOut)
	}
	var preview kernel.ArtifactPreview
	if err := json.Unmarshal([]byte(previewOut), &preview); err != nil {
		t.Fatalf("read-artifact output is not JSON: %v (%s)", err, previewOut)
	}
	if preview.Content != "0123" || !preview.Truncated || preview.MaxBytes != 4 {
		t.Fatalf("read-artifact ignored its byte bound: %+v", preview)
	}
}

func TestCLIInvestigationMakesPlanTheFirstContinuation(t *testing.T) {
	ws := cliRepo(t)
	opened := qaRunCLIEnvelope(t, "--json", "-C", ws, "open", "trace the callback path",
		"--actor", "agent-a", "--idempotency-key", "qa-investigation-order")
	qaRequireCLIAction(t, opened, "cortex_investigate")

	investigated := qaRunCLIEnvelope(t, "--json", "-C", ws, "investigate", opened.TaskID, "HandleCallback")
	plan := qaRequireCLIAction(t, investigated, "cortex_plan")
	if plan.Arguments["taskId"] != opened.TaskID || plan.Arguments["workspace"] != ws {
		t.Fatalf("plan continuation is not portable: %+v", plan)
	}
	if len(investigated.Actions) != 2 || investigated.Actions[1].Tool != "cortex_investigate" {
		t.Fatalf("post-investigation choices = %+v, want plan then optional investigation", investigated.Actions)
	}
}

func TestCLIAgentHelpMatchesExposedContracts(t *testing.T) {
	serve, err := runCLI(t, "serve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"17 focused", "24-tool", "open_task", "begin_change", "same actor"} {
		if !strings.Contains(serve, want) {
			t.Errorf("serve help missing %q:\n%s", want, serve)
		}
	}

	verify, err := runCLI(t, "verify", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--actor", "--claim-surface", "--claim-contract", "same --actor"} {
		if !strings.Contains(verify, want) {
			t.Errorf("verify help missing %q:\n%s", want, verify)
		}
	}

	open, err := runCLI(t, "open", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(open, "even after completion") {
		t.Errorf("open help does not explain durable retry identity:\n%s", open)
	}
}

func qaRunCLIEnvelope(t *testing.T, args ...string) domain.Envelope {
	t.Helper()
	out, err := runCLI(t, args...)
	if err != nil {
		t.Fatalf("cortex %s: %v (%s)", strings.Join(args, " "), err, out)
	}
	var env domain.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("cortex %s did not emit an envelope: %v (%s)", strings.Join(args, " "), err, out)
	}
	if !env.OK {
		t.Fatalf("cortex %s returned ok:false: %+v", strings.Join(args, " "), env)
	}
	return env
}

func qaRequireCLIAction(t *testing.T, env domain.Envelope, wantTool string) domain.NextAction {
	t.Helper()
	if len(env.Actions) == 0 || env.Actions[0].Tool != wantTool {
		t.Fatalf("first action = %+v, want tool %s", env.Actions, wantTool)
	}
	return env.Actions[0]
}

func qaCLITypedReceipt(receipts []domain.VerificationRecord, surface domain.Surface, contract string) bool {
	for _, receipt := range receipts {
		if receipt.EffectivePurpose() == domain.VerificationPurposeNamedClaim && receipt.Surface == surface && receipt.Contract == contract {
			return true
		}
	}
	return false
}

func qaCLIStringContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
