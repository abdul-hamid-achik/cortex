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
		return errEnvelope("", "a goal is required to open a task"), nil
	}
	in.Goal = k.red.String(strings.TrimSpace(in.Goal))
	mode, ok := normalizeMode(in.Mode)
	if !ok {
		return errEnvelope("", k.red.String(fmt.Sprintf("mode must be one of: change, investigate, review (got %q)", in.Mode))), nil
	}
	risk, ok := normalizeRisk(in.Risk)
	if !ok {
		return errEnvelope("", k.red.String(fmt.Sprintf("risk must be one of: low, medium, high (got %q)", in.Risk))), nil
	}
	surfaces, err := normalizeSurfaces(in.Surfaces)
	if err != nil {
		return errEnvelope("", k.red.String(err.Error())), nil
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
			return errEnvelope("", "parent task: "+loadErr.Error()), nil
		}
		if parent.Workspace.Root != k.cfg.Workspace {
			return errEnvelope("", "parent task belongs to a different workspace"), nil
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
				result, finishErr = k.finishOrientation(ctx, c, true)
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
