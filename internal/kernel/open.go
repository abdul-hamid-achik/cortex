package kernel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// OpenInput is an idempotent StartInput. An explicit key is the strongest
// identity; without one, Cortex resumes the newest active case with the same
// normalized goal, mode, workspace, and current branch.
type OpenInput struct {
	StartInput
}

// OpenTask resumes matching work or starts it exactly once. It is safe for an
// agent to retry after losing a tool response.
func (k *Kernel) OpenTask(ctx context.Context, in OpenInput) (domain.Envelope, error) {
	if strings.TrimSpace(in.Goal) == "" {
		return k.errEnvelopeActions("", "a goal is required to open a task",
			k.openContinuation("cortex_open_task", "open", in.StartInput, "goal", nil)), nil
	}
	in.Goal = k.red.String(strings.TrimSpace(in.Goal))
	mode, ok := normalizeMode(in.Mode)
	if !ok {
		return k.errEnvelopeActions("", k.red.String(fmt.Sprintf("mode must be one of: change, investigate, review (got %q)", in.Mode)),
			k.openContinuation("cortex_open_task", "open", in.StartInput, "mode", map[string][]string{"mode": {"change", "investigate", "review"}})), nil
	}
	risk, ok := normalizeRisk(in.Risk)
	if !ok {
		return k.errEnvelopeActions("", k.red.String(fmt.Sprintf("risk must be one of: low, medium, high (got %q)", in.Risk)),
			k.openContinuation("cortex_open_task", "open", in.StartInput, "risk", map[string][]string{"risk": {"low", "medium", "high"}})), nil
	}
	surfaces, err := normalizeSurfaces(in.Surfaces)
	if err != nil {
		return k.errEnvelopeActions("", k.red.String(err.Error()),
			k.openContinuation("cortex_open_task", "open", in.StartInput, "surfaces", map[string][]string{"surfaces": {"code", "browser", "terminal", "artifact", "secret"}})), nil
	}
	criteria, err := k.normalizeAcceptanceCriteria(in.AcceptanceCriteria)
	if err != nil {
		return errEnvelope("", err.Error()), nil
	}
	_, parentTaskID, key, err := k.normalizeTaskMetadata(in.Actor, in.ParentTaskID, in.IdempotencyKey)
	if err != nil {
		return errEnvelope("", err.Error()), nil
	}
	if parentTaskID != "" {
		parent, loadErr := k.store.Load(parentTaskID)
		if loadErr != nil {
			return k.errEnvelopeActions("", "parent task: "+loadErr.Error(),
				k.openContinuation("cortex_open_task", "open", in.StartInput, "parentTaskId", nil)), nil
		}
		if parent.Workspace.Root != k.cfg.Workspace {
			return k.errEnvelopeActions("", "parent task belongs to a different workspace",
				k.openContinuation("cortex_open_task", "open", in.StartInput, "parentTaskId", nil)), nil
		}
		parentTaskID = parent.ID
		in.ParentTaskID = parent.ID
	}
	branch := ""
	if k.git != nil {
		if status, statusErr := k.git.Status(ctx, k.cfg.Workspace); statusErr == nil {
			branch = status.Branch
		}
	}
	in.Mode = mode
	in.Risk = risk
	in.Surfaces = surfaces
	in.AcceptanceCriteria = criteria
	identity := openCoordinationIdentity(k.cfg.Workspace, in.Goal, mode, branch, parentTaskID, key)
	var result domain.Envelope
	err = k.store.WithCoordinationLock(identity, func() error {
		// Re-scan while holding the identity lock. This closes the classic
		// check-then-create race across the per-call Store instances used by MCP.
		candidates, candidateErr := k.openCandidates(in.Goal, mode, branch, parentTaskID, key, criteria)
		if candidateErr != nil {
			return candidateErr
		}
		if len(candidates) > 0 {
			c := candidates[0]
			if key != "" && !acceptanceCriteriaEqual(c.AcceptanceCriteria, criteria) {
				result = errEnvelope(c.ID, "idempotency key already identifies a task with different acceptance criteria")
				return nil
			}
			if c.Status == domain.PhaseNew || c.Status == domain.PhaseOrienting {
				var finishErr error
				result, finishErr = k.finishOrientation(ctx, c, true, nil)
				return finishErr
			}
			warnings := []string(nil)
			if len(candidates) > 1 {
				warnings = append(warnings, fmt.Sprintf("%d active cases matched; resumed the most recently updated", len(candidates)))
			}
			// Parent and child snapshots are separate CAS writes. If the original
			// start lost the parent-link race, an idempotent open is the repair path.
			if c.ParentTaskID != "" {
				if linkErr := k.linkParentChild(c.ParentTaskID, c.ID); linkErr != nil {
					warnings = append(warnings, "parent linkage still needs repair: "+linkErr.Error())
				}
			}
			// Re-project Bob orientation on the public idempotent retry path. The
			// evidence/raw identities are digest-stable, so this preserves the
			// original guidance or degradation without duplicating durable records.
			bob, bobErr := k.orientWithBob(ctx, c)
			if bobErr != nil {
				result = errEnvelope(c.ID, "orientation canceled before projection: "+bobErr.Error())
				return bobErr
			}
			warnings = append(warnings, bob.warnings...)
			result = k.envelope(c, fmt.Sprintf("resumed existing task %s (%s)", c.ID, c.Goal), bob.facts, warnings, nextForPhase(c.Status))
			if len(bob.actions) > 0 {
				result.Actions = append(k.redactStructuredActions(bob.actions), result.Actions...)
			}
			result.Degraded = bob.degraded
			return nil
		}
		var startErr error
		result, startErr = k.StartTask(ctx, in.StartInput)
		return startErr
	})
	if err != nil {
		if result.Summary == "" {
			result = errEnvelope("", err.Error())
		}
		return result, err
	}
	return result, nil
}

func openCoordinationIdentity(workspace, goal string, mode domain.Mode, branch, parentTaskID, key string) string {
	if key != "" {
		return "open-key\x00" + workspace + "\x00" + key
	}
	return "open-goal\x00" + workspace + "\x00" + branch + "\x00" + string(mode) + "\x00" + parentTaskID + "\x00" + normalizeGoal(goal)
}

func (k *Kernel) openCandidates(goal string, mode domain.Mode, branch, parentTaskID, key string, criteria []domain.AcceptanceCriterion) ([]*domain.CaseFile, error) {
	ids, err := k.store.List()
	if err != nil {
		return nil, err
	}
	normalizedGoal := normalizeGoal(goal)
	var out []*domain.CaseFile
	for _, id := range ids {
		c, loadErr := k.store.Load(id)
		if loadErr != nil || c.Workspace.Root != k.cfg.Workspace {
			continue
		}
		if key != "" {
			if c.IdempotencyKey == key {
				out = append(out, c)
			}
			continue
		}
		if !acceptanceCriteriaEqual(c.AcceptanceCriteria, criteria) {
			continue
		}
		if c.Status.IsTerminal() || c.Mode != mode || c.ParentTaskID != parentTaskID || normalizeGoal(c.Goal) != normalizedGoal {
			continue
		}
		if branch != "" && c.Workspace.Branch != "" && c.Workspace.Branch != branch {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func normalizeGoal(goal string) string {
	return strings.ToLower(strings.Join(strings.Fields(goal), " "))
}

// openContinuation builds a retryable cortex_open_task/cortex_start_task
// continuation: it pre-fills every value the request already supplied
// (skipping only the field named by missing, since that one is exactly what's
// wrong) and names what still needs fixing. Candidates, when supplied, are
// mode/risk/surface's own fixed vocabulary — the identical literal set the
// rejection's prose already names — never invented content.
func (k *Kernel) openContinuation(tool, verb string, in StartInput, missing string, candidates map[string][]string) domain.NextAction {
	goal := strings.TrimSpace(in.Goal)
	mode := string(in.Mode)
	risk := in.Risk
	args := map[string]any{}
	if missing != "goal" && goal != "" {
		args["goal"] = goal
	}
	if missing != "mode" && mode != "" {
		args["mode"] = mode
	}
	if missing != "risk" && risk != "" {
		args["risk"] = risk
	}
	if missing != "surfaces" && len(in.Surfaces) > 0 {
		surfaces := make([]string, 0, len(in.Surfaces))
		for _, s := range in.Surfaces {
			surfaces = append(surfaces, string(s))
		}
		args["surfaces"] = surfaces
	}
	if in.Actor != "" {
		args["actor"] = in.Actor
	}
	if missing != "parentTaskId" && in.ParentTaskID != "" {
		args["parentTaskId"] = in.ParentTaskID
	}
	if in.IdempotencyKey != "" {
		args["idempotencyKey"] = in.IdempotencyKey
	}

	command := []string{verb, firstNonEmptyStr(goal, "GOAL")}
	if missing == "mode" {
		command = append(command, "--mode", "MODE")
	} else if mode != "" {
		command = append(command, "--mode", mode)
	}
	if missing == "risk" {
		command = append(command, "--risk", "RISK")
	} else if risk != "" {
		command = append(command, "--risk", risk)
	}
	if missing == "surfaces" {
		command = append(command, "--surface", "SURFACE")
	} else {
		for _, s := range in.Surfaces {
			command = append(command, "--surface", string(s))
		}
	}
	if in.Actor != "" {
		command = append(command, "--actor", in.Actor)
	}
	if missing == "parentTaskId" {
		command = append(command, "--parent", firstNonEmptyStr(in.ParentTaskID, "PARENT_TASK_ID"))
	} else if in.ParentTaskID != "" {
		command = append(command, "--parent", in.ParentTaskID)
	}
	if in.IdempotencyKey != "" {
		command = append(command, "--idempotency-key", in.IdempotencyKey)
	}
	// No case exists yet, but the workspace this call would open into is
	// already known — reuse it for the same -C-pinned, shell-safe rendering
	// every other structured command gets.
	pseudo := &domain.CaseFile{Workspace: domain.Workspace{Root: k.cfg.Workspace}}
	action := domain.NextAction{
		Tool: tool, Command: cortexCommand(pseudo, command...),
		Reason: "supply the missing or corrected field and retry " + verb, Arguments: args, Inputs: []string{missing},
	}
	if len(candidates) > 0 {
		action.Candidates = candidates
	}
	return action
}

func (k *Kernel) normalizeTaskMetadata(actor, parentTaskID, idempotencyKey string) (string, string, string, error) {
	values := []*string{&actor, &parentTaskID, &idempotencyKey}
	labels := []string{"actor", "parent task id", "idempotency key"}
	for i, value := range values {
		*value = strings.TrimSpace(*value)
		if len(*value) > 256 {
			return "", "", "", fmt.Errorf("%s is too long", labels[i])
		}
		if *value != "" && k.red.Detected(*value) {
			return "", "", "", fmt.Errorf("%s must be a stable non-sensitive identifier", labels[i])
		}
		if i == 0 && *value != "" && !validActorIdentifier(*value) {
			return "", "", "", fmt.Errorf("actor may contain only letters, digits, dash, underscore, dot, slash, colon, or at-sign")
		}
		*value = k.red.String(*value)
	}
	return actor, parentTaskID, idempotencyKey, nil
}

func validActorIdentifier(actor string) bool {
	for _, r := range actor {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '@':
			continue
		default:
			return false
		}
	}
	return actor != ""
}

func (k *Kernel) linkParentChild(parentTaskID, childTaskID string) error {
	for attempt := 0; attempt < maxLeaseCASAttempts; attempt++ {
		parent, err := k.store.Load(parentTaskID)
		if err != nil {
			return err
		}
		for _, existing := range parent.ChildTaskIDs {
			if existing == childTaskID {
				return nil
			}
		}
		parent.ChildTaskIDs = append(parent.ChildTaskIDs, childTaskID)
		if err := k.store.Save(parent); err != nil {
			if errors.Is(err, casefs.ErrRevisionConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("parent task changed concurrently; retry open")
}
