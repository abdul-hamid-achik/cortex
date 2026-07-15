package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

const (
	maxBobPathCalls         = 16
	maxBobPlanActions       = 32
	maxBobPlanWarnings      = 64
	maxBobPlanNotes         = 32
	maxBobPlanNoteBytes     = 1024
	maxBobWarningPathBytes  = 512
	bobBoundaryTotalTimeout = 30 * time.Second
)

type bobManifestCheck struct {
	present  bool
	runnable bool
	problem  string
}

// checkBobManifest distinguishes an absent opt-in from a present but unsafe or
// unreadable manifest path. Regular files (including symlinks resolving to one)
// may be handed to Bob for schema validation; other node types degrade without
// opening them, so a FIFO cannot stall orientation.
func checkBobManifest(workspace string) bobManifestCheck {
	path := filepath.Join(workspace, "bob.yaml")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return bobManifestCheck{}
	}
	if err != nil {
		return bobManifestCheck{present: true, problem: "cannot inspect bob.yaml: " + err.Error()}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, statErr := os.Stat(path)
		if statErr != nil {
			return bobManifestCheck{present: true, problem: "bob.yaml symlink cannot be resolved: " + statErr.Error()}
		}
		if target.Mode().IsRegular() {
			return bobManifestCheck{present: true, runnable: true}
		}
		return bobManifestCheck{present: true, problem: "bob.yaml symlink does not resolve to a regular file"}
	}
	if !info.Mode().IsRegular() {
		return bobManifestCheck{present: true, problem: "bob.yaml must be a regular file"}
	}
	return bobManifestCheck{present: true, runnable: true}
}

func bobManifestRunnable(workspace string) bool {
	return checkBobManifest(workspace).runnable
}

type bobOrientationResult struct {
	facts    []domain.Evidence
	warnings []string
	actions  []domain.NextAction
	degraded bool
}

func (k *Kernel) orientWithBob(ctx context.Context, c *domain.CaseFile) (bobOrientationResult, error) {
	if err := ctx.Err(); err != nil {
		return bobOrientationResult{}, err
	}
	if c == nil {
		return bobOrientationResult{}, nil
	}
	manifest := checkBobManifest(k.cfg.Workspace)
	if !manifest.present {
		return bobOrientationResult{}, nil
	}
	if !manifest.runnable {
		out := bobOrientationResult{
			warnings: []string{"Bob repository contract was not assessed: " + manifest.problem},
			actions:  []domain.NextAction{bobContextAction(c, "repair bob.yaml before retrying repository-contract orientation", "invalid bob.yaml")},
			degraded: true,
		}
		// A present but unsafe manifest node is itself a repository-contract
		// observation. Retain that negative result with a digest-derived identity
		// without opening the node or invoking Bob, so retries and handoffs can
		// audit why orientation degraded without duplicating evidence.
		suffix := bobIdentity("context", "manifest_invalid", manifest.problem)
		fact := adapters.Fact{
			Kind:       "repository_contract",
			Claim:      "Bob repository contract was not assessed: " + manifest.problem,
			Confidence: "unknown",
			URI:        "bob://manifest/local/" + suffix,
			Attributes: map[string]string{"error_code": "manifest_invalid", "error_message": manifest.problem},
		}
		ev, err := k.stampEvidenceOnce(c.ID, "ev_bob_context_"+suffix, "bob", fact, c.CreatedAt)
		if err != nil {
			out.warnings = append(out.warnings, "Bob invalid-manifest evidence could not be retained: "+err.Error())
		} else {
			out.facts = append(out.facts, ev)
		}
		return out, nil
	}
	if k.reg.Get("bob") == nil {
		return bobOrientationResult{
			warnings: []string{"bob.yaml is present but the optional Bob adapter is unavailable; repository-contract orientation was not assessed"},
			actions:  []domain.NextAction{bobContextAction(c, "retry Bob repository-contract orientation", "Bob adapter unavailable")},
			degraded: true,
		}, nil
	}

	res := k.run(ctx, "bob", adapters.Request{
		TaskID: c.ID, Operation: "context",
		Input: map[string]any{"workspace": k.cfg.Workspace, "profile": "compact"},
	})
	if err := ctx.Err(); err != nil {
		return bobOrientationResult{}, err
	}
	out := bobOrientationResult{degraded: res.Status != adapters.StatusAuthoritative}
	for _, warning := range res.Warnings {
		out.warnings = append(out.warnings, "Bob repository contract: "+warning)
	}

	fact, ok := firstBobFact(res.Facts, "")
	if !ok {
		if res.Status == adapters.StatusUnavailable {
			fact = adapters.Fact{
				Kind: "tool_unavailable", Confidence: "unknown",
				Claim: "Bob is unavailable; repository desired state and ownership were not assessed",
			}
			ok = true
		}
	}
	if ok {
		identity := fact.Attributes["context_digest"]
		if identity == "" {
			identity = strings.Join([]string{fact.Attributes["error_code"], fact.Attributes["error_message"], res.Summary, fact.Claim}, "\x00")
		}
		suffix := bobIdentity("context", identity)
		evidenceID := "ev_bob_context_" + suffix
		if durable, err := k.store.GetEvidence(c.ID, evidenceID); err == nil {
			out.facts = append(out.facts, durable)
		} else if !errors.Is(err, casefs.ErrNotFound) {
			out.warnings = append(out.warnings, "Bob repository-contract evidence could not be read: "+err.Error())
			out.degraded = true
		} else {
			rawRef := k.storeRawStable(c.ID, "raw_bob_context_"+suffix, res)
			ev, stampErr := k.stampEvidenceOnceRaw(c.ID, evidenceID, "bob", fact, rawRef, k.now().UTC())
			if stampErr != nil {
				out.warnings = append(out.warnings, "Bob repository-contract evidence could not be retained: "+stampErr.Error())
				out.degraded = true
			} else {
				out.facts = append(out.facts, ev)
			}
		}
	}

	usablePartial := res.Status == adapters.StatusPartial && fact.Attributes["context_digest"] != "" && fact.Attributes["bob_truncated"] == "true"
	if res.Status != adapters.StatusAuthoritative && !usablePartial {
		blockedBy := "Bob repository-contract query failed"
		if fact.Attributes["error_code"] == "manifest_invalid" || fact.Attributes["error_code"] == "missing_manifest" {
			blockedBy = "invalid bob.yaml"
		}
		if res.Status == adapters.StatusUnavailable {
			blockedBy = "Bob binary unavailable"
		}
		out.actions = append(out.actions, bobContextAction(c, "retry Bob repository-contract orientation after correcting the degraded condition", blockedBy))
		if len(out.warnings) == 0 {
			out.warnings = append(out.warnings, "Bob repository-contract orientation was inconclusive; Cortex work may continue without treating Bob as proof")
		}
	}
	return out, nil
}

type bobPathCapture struct {
	path      string
	result    adapters.Result
	fact      adapters.Fact
	captured  time.Time
	stableKey string
}

type bobBoundaryResult struct {
	captures []bobPathCapture
	warnings []string
	actions  []domain.NextAction
	degraded bool
}

// inspectBobBoundary performs a read-only, strictly-budgeted projection. It
// stages captures in memory; callers persist them only after the plan CAS wins.
func (k *Kernel) inspectBobBoundary(ctx context.Context, c *domain.CaseFile, files []string) bobBoundaryResult {
	if c == nil || len(files) == 0 {
		return bobBoundaryResult{}
	}
	manifest := checkBobManifest(k.cfg.Workspace)
	if !manifest.present {
		return bobBoundaryResult{}
	}
	if !manifest.runnable {
		return bobBoundaryResult{warnings: []string{"Bob path ownership was not assessed: " + manifest.problem}, degraded: true}
	}
	if k.reg.Get("bob") == nil {
		return bobBoundaryResult{
			warnings: []string{"Bob path ownership was not assessed because the optional adapter is unavailable"},
			degraded: true,
		}
	}

	paths := dedupeStr(files)
	result := bobBoundaryResult{}
	if len(paths) > maxBobPathCalls {
		result.warnings = append(result.warnings, fmt.Sprintf(
			"Bob path review is capped at %d calls; %d of %d declared files were not classified",
			maxBobPathCalls, len(paths)-maxBobPathCalls, len(paths)))
		paths = paths[:maxBobPathCalls]
		result.degraded = true
	}
	seenPlaybooks := make(map[string]bool)
	ctx, cancel := context.WithTimeout(ctx, bobBoundaryTotalTimeout)
	defer cancel()
	for index, path := range paths {
		res := k.run(ctx, "bob", adapters.Request{
			TaskID: c.ID, Operation: "path",
			Input: map[string]any{"workspace": k.cfg.Workspace, "path": path},
		})
		for _, warning := range res.Warnings {
			result.warnings = append(result.warnings, fmt.Sprintf("Bob path %s: %s", path, warning))
		}
		fact, ok := firstBobPathFact(res.Facts, path)
		if !ok || res.Status != adapters.StatusAuthoritative && res.Status != adapters.StatusPartial {
			result.degraded = true
			unreviewed := len(paths) - index
			result.warnings = append(result.warnings, fmt.Sprintf(
				"Bob path ownership became unavailable at %s; %d declared path(s) remain unclassified", path, unreviewed))
			if len(result.actions) < maxBobPlanActions {
				action := bobPathAction(c, path, "confirm Bob ownership after restoring repository-contract access")
				action.BlockedBy = []string{"Bob path query unavailable"}
				result.actions = append(result.actions, action)
			}
			break
		}

		capture := bobPathCapture{
			path: path, result: res, fact: fact, captured: k.now().UTC(),
			stableKey: bobIdentity("path", path, fact.URI, fact.Claim),
		}
		result.captures = append(result.captures, capture)
		if res.Status == adapters.StatusPartial || fact.Attributes["bob_truncated"] == "true" {
			result.degraded = true
		}

		effect := fact.Attributes["human_edit_effect"]
		warning, reason := bobBoundaryWarning(path, effect)
		playbooks := bobStringArray(fact.Attributes["related_playbooks"])
		if warning != "" {
			if len(playbooks) > 0 {
				warning += fmt.Sprintf("; Bob returned playbook %s", playbooks[0])
			}
			result.warnings = append(result.warnings, warning)
			if len(result.actions) < maxBobPlanActions {
				result.actions = append(result.actions, bobPathAction(c, path, reason))
			}
		}
		for _, id := range playbooks {
			if strings.TrimSpace(id) == "" || seenPlaybooks[id] || len(result.actions) >= maxBobPlanActions {
				continue
			}
			seenPlaybooks[id] = true
			result.actions = append(result.actions, bobPlaybookAction(c, id, path))
		}
	}
	result.warnings = boundedBobWarnings(result.warnings)
	return result
}

// stageBobBoundary creates retry-stable evidence and raw records without
// writing them. Plan publishes this complete set with the case/plan snapshots
// in one revisioned filesystem transaction. Existing stable evidence is reused
// byte-for-byte so a later re-plan cannot mutate its provenance timestamp or
// raw capture.
func (k *Kernel) stageBobBoundary(c *domain.CaseFile, captures []bobPathCapture) ([]domain.Evidence, []casefs.RawRecord, []string) {
	var facts []domain.Evidence
	var raws []casefs.RawRecord
	var warnings []string
	for _, capture := range captures {
		evidenceID := "ev_bob_path_" + capture.stableKey
		if durable, err := k.store.GetEvidence(c.ID, evidenceID); err == nil {
			facts = append(facts, durable)
			continue
		} else if !errors.Is(err, casefs.ErrNotFound) {
			warnings = append(warnings, fmt.Sprintf("Bob path evidence for %s could not be staged: %v", capture.path, err))
			continue
		}

		rawRef := ""
		if capture.result.Raw != "" {
			rawID := "raw_bob_path_" + capture.stableKey
			raw := capRawForStore(k.red.String(capture.result.Raw), k.cfg.Budget.MaxRawOutputBytesPerTool)
			if durable, err := k.store.ReadRaw(c.ID, rawID); err == nil {
				raw = durable
			} else if !errors.Is(err, casefs.ErrNotFound) {
				warnings = append(warnings, fmt.Sprintf("Bob path raw for %s could not be staged: %v", capture.path, err))
				raw = ""
			}
			if raw != "" {
				raws = append(raws, casefs.RawRecord{ID: rawID, Content: raw})
				rawRef = fmt.Sprintf("case://%s/raw/%s", c.ID, rawID)
			}
		}
		ev := k.buildEvidenceDerived(c.ID, "bob", capture.fact, rawRef, nil)
		ev.ID = evidenceID
		ev.Timestamp = capture.captured.UTC()
		if rawRef == "" {
			ev.RawRef = fmt.Sprintf("case://%s/evidence/%s", c.ID, evidenceID)
		}
		facts = append(facts, ev)
	}
	return facts, raws, boundedBobWarnings(warnings)
}

func boundedBobWarnings(values []string) []string {
	values = dedupeStr(values)
	limit := len(values)
	if limit > maxBobPlanWarnings {
		limit = maxBobPlanWarnings - 1
	}
	out := make([]string, 0, min(len(values), maxBobPlanWarnings))
	for _, value := range values[:limit] {
		bounded, _ := boundedUTF8(value, maxBobPlanNoteBytes)
		out = append(out, bounded)
	}
	if limit < len(values) {
		out = append(out, fmt.Sprintf("%d additional Bob warning(s) omitted by the planning bound", len(values)-limit))
	}
	return out
}

func (k *Kernel) retainBobBoundaryNotes(c *domain.CaseFile, warnings []string) {
	if c == nil {
		return
	}
	count := 0
	for _, note := range c.Notes {
		if strings.HasPrefix(note, "Bob boundary: ") {
			count++
		}
	}
	for _, warning := range boundedBobWarnings(warnings) {
		if count >= maxBobPlanNotes {
			break
		}
		note, _ := boundedUTF8("Bob boundary: "+k.red.String(warning), maxBobPlanNoteBytes)
		before := len(c.Notes)
		c.Notes = dedupeStr(append(c.Notes, note))
		if len(c.Notes) > before {
			count++
		}
	}
}

func firstBobFact(facts []adapters.Fact, path string) (adapters.Fact, bool) {
	for _, fact := range facts {
		if fact.Kind != "repository_contract" && fact.Kind != "tool_unavailable" {
			continue
		}
		if path != "" && fact.Attributes["path"] != "" && fact.Attributes["path"] != path {
			continue
		}
		return fact, true
	}
	return adapters.Fact{}, false
}

func firstBobPathFact(facts []adapters.Fact, path string) (adapters.Fact, bool) {
	for _, fact := range facts {
		if fact.Kind != "repository_contract" || fact.Attributes["path"] != path || fact.Attributes["classification"] == "" || fact.Attributes["human_edit_effect"] == "" {
			continue
		}
		return fact, true
	}
	return adapters.Fact{}, false
}

func bobBoundaryWarning(path, effect string) (warning, reason string) {
	displayPath, truncated := boundedUTF8(path, maxBobWarningPathBytes)
	if truncated {
		displayPath += "…"
	}
	switch effect {
	case "will_conflict":
		return fmt.Sprintf("Bob-managed path %s will conflict with Bob ownership if edited by a human", displayPath), "confirm Bob ownership before editing a managed file"
	case "reserved_for_bob":
		return fmt.Sprintf("Bob-reserved path %s should not be edited as application code", displayPath), "confirm why the path is reserved before editing"
	case "requires_manifest_change":
		return fmt.Sprintf("Bob manifest-controlled path %s requires a reviewed bob.yaml contract change rather than a generated-file edit", displayPath), "review the Bob manifest operation required for this path"
	case "unsafe":
		return fmt.Sprintf("Bob-unsafe repository-contract path %s must be inspected before editing", displayPath), "inspect the unsafe Bob path classification before editing"
	default:
		// outside_bob_ownership is intentionally silent: it is not a statement
		// that the path is globally safe, only that Bob does not own it.
		return "", ""
	}
}

func bobContextAction(c *domain.CaseFile, reason, blockedBy string) domain.NextAction {
	action := domain.NextAction{
		Tool: "bob_context", Reason: reason,
		Command:   bobCommand("--json", "context", c.Workspace.Root, "--profile", "compact"),
		Arguments: map[string]any{"workspace": c.Workspace.Root, "profile": "compact"},
	}
	if blockedBy != "" {
		action.BlockedBy = []string{blockedBy}
	}
	return action
}

func bobPathAction(c *domain.CaseFile, path, reason string) domain.NextAction {
	return domain.NextAction{
		Tool: "bob_path", Reason: reason,
		Command:   bobCommand("--json", "path", "--workspace", c.Workspace.Root, "--", path),
		Arguments: map[string]any{"workspace": c.Workspace.Root, "path": path},
	}
}

func bobPlaybookAction(c *domain.CaseFile, id, path string) domain.NextAction {
	return domain.NextAction{
		Tool: "bob_playbook", Reason: fmt.Sprintf("review Bob playbook %s returned for planned path %s", id, path),
		Command:   bobCommand("--json", "playbook", "show", id, c.Workspace.Root),
		Arguments: map[string]any{"workspace": c.Workspace.Root, "id": id, "operation": "show"},
	}
}

func bobCommand(args ...string) string {
	parts := []string{"bob"}
	for _, arg := range args {
		parts = append(parts, shellArg(arg))
	}
	return strings.Join(parts, " ")
}

func bobIdentity(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:20]
}

func bobStringArray(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil
	}
	return dedupeStr(values)
}
