package kernel

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// StartInput parameterizes StartTask.
type StartInput struct {
	Goal               string
	Mode               domain.Mode
	Surfaces           []domain.Surface
	AcceptanceCriteria []domain.AcceptanceCriterion
	Risk               string
	BaseRef            string // diff base for a review task (empty = working-tree diff)
	Actor              string
	ParentTaskID       string
	IdempotencyKey     string
}

// StartTask creates a case file and performs lightweight orientation: it reads
// git identity and probes tool health, then advances the task from new through
// orienting to investigating.
func (k *Kernel) StartTask(ctx context.Context, in StartInput) (domain.Envelope, error) {
	goal := strings.TrimSpace(in.Goal)
	if goal == "" {
		return errEnvelope("", "a goal is required to start a task"), nil
	}
	if textExceeds(goal, maxGoalBytes) {
		return errEnvelope("", fmt.Sprintf("goal exceeds %d bytes", maxGoalBytes)), nil
	}
	if textExceeds(strings.TrimSpace(in.BaseRef), maxLocatorBytes) {
		return errEnvelope("", fmt.Sprintf("base ref exceeds %d bytes", maxLocatorBytes)), nil
	}
	goal = k.red.String(goal)
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
	actor, parentTaskID, idempotencyKey, err := k.normalizeTaskMetadata(in.Actor, in.ParentTaskID, in.IdempotencyKey)
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
		// Store lookup sanitizes path-like input defensively; persist the case's
		// canonical minted ID so linkage never retains an alias such as task/x.
		parentTaskID = parent.ID
	}
	c := &domain.CaseFile{
		SchemaVersion:      domain.SchemaVersion,
		ID:                 ids.New("task"),
		CreatedAt:          k.now().UTC(),
		Goal:               goal,
		Mode:               mode,
		Status:             domain.PhaseNew,
		Risk:               risk,
		Surfaces:           surfaces,
		AcceptanceCriteria: criteria,
		Workspace:          domain.Workspace{Root: k.cfg.Workspace, Repository: filepath.Base(k.cfg.Workspace), BaseRef: in.BaseRef},
		Actor:              actor,
		ParentTaskID:       parentTaskID,
		IdempotencyKey:     idempotencyKey,
	}

	// Persist the case skeleton FIRST. Appending to any ledger — phases.jsonl via
	// the transition below, or evidence via stampEvidence — creates the task
	// directory, so Create must run before them or it would see the directory
	// already present and refuse ("case already exists").
	if err := k.store.Create(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	return k.finishOrientation(ctx, c, false)
}

// finishOrientation completes a persisted new/orienting skeleton. OpenTask
// calls it after a crash, so response loss during start cannot strand a case in
// a phase with no continuation.
func (k *Kernel) finishOrientation(ctx context.Context, c *domain.CaseFile, resumed bool) (domain.Envelope, error) {
	type phaseMove struct{ from, to domain.Phase }
	var moves []phaseMove
	// new → orienting: a goal and workspace exist.
	if c.Status == domain.PhaseNew {
		from := c.Status
		if err := k.transition(c, domain.PhaseOrienting); err != nil {
			return errEnvelope(c.ID, err.Error()), nil
		}
		moves = append(moves, phaseMove{from: from, to: c.Status})
	} else if c.Status != domain.PhaseOrienting {
		return errEnvelope(c.ID, fmt.Sprintf("cannot finish orientation in phase %q", c.Status)), nil
	}

	var facts []domain.Evidence
	var warnings []string

	// Git orientation: workspace identity and baseline commit.
	if k.git != nil {
		if info, err := k.git.Status(ctx, k.cfg.Workspace); err == nil && info.IsRepo {
			c.Workspace.Repository = info.Repository
			c.Workspace.Branch = info.Branch
			c.Workspace.CommitBefore = info.Commit
			claim := fmt.Sprintf("workspace %s on branch %s at %s", info.Repository, info.Branch, info.Commit)
			if info.Dirty {
				claim += " (working tree already dirty)"
				warnings = append(warnings, "working tree is dirty at task start — baseline diff may include unrelated edits")
			}
			if ev, err := k.stampEvidenceOnce(c.ID, "ev_orientation_git", "git", adapters.Fact{Kind: "code_location", Claim: claim, Confidence: "high"}, c.CreatedAt); err == nil {
				facts = append(facts, ev)
			}
		} else {
			warnings = append(warnings, "workspace is not a git repository — scope-drift detection and diff review are limited")
		}
	}

	// Tool health snapshot is an orientation precondition.
	health := k.reg.Health(ctx)
	var down []string
	for _, h := range health {
		if !h.Available {
			down = append(down, h.Tool)
		}
	}
	healthSummary := healthNote(health)
	c.Notes = dedupeStr(append(c.Notes, healthSummary))
	if len(down) > 0 {
		warnings = append(warnings, "tools unavailable: "+joinStr(down, ", ")+" — verification on their surfaces will be blocked")
	}

	// Cross-case disproof recall surfaces prior related cases as
	// low-confidence orientation so a weak model reads prior disproofs before
	// re-deriving a theory. Best-effort — a missing veclite is warn-once.
	prior, recallWarn, nPrior := k.recallPriorCases(ctx, c, c.Goal, 5)
	facts = append(facts, prior...)
	if recallWarn != "" {
		warnings = append(warnings, recallWarn)
	}

	// orienting → investigating: identity and tool health are known.
	from := c.Status
	if err := k.transition(c, domain.PhaseInvestigating); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	moves = append(moves, phaseMove{from: from, to: c.Status})
	saved := false
	if err := k.store.Save(c); err != nil {
		if errors.Is(err, casefs.ErrRevisionConflict) {
			latest, loadErr := k.store.Load(c.ID)
			if loadErr == nil && latest.Status == domain.PhaseInvestigating {
				c = latest
				warnings = append(warnings, "orientation was completed by a concurrent opener")
			} else {
				return errEnvelope(c.ID, err.Error()), err
			}
		} else {
			return errEnvelope(c.ID, err.Error()), err
		}
	} else {
		saved = true
	}
	if saved {
		for _, move := range moves {
			k.recordPhase(c.ID, move.from, move.to)
		}
	}
	if c.ParentTaskID != "" {
		if err := k.linkParentChild(c.ParentTaskID, c.ID); err != nil {
			warnings = append(warnings, "task started but parent linkage needs repair: "+err.Error())
		}
	}

	next := []string{
		"cortex investigate — discover by meaning, then resolve structure",
		"treat search output as candidates, not proof",
	}
	if nPrior > 0 {
		next = append([]string{fmt.Sprintf("%d prior related case(s) recalled — read before re-deriving a theory", nPrior)}, next...)
	}
	verb := "started"
	if resumed {
		verb = "recovered"
	}
	env := k.envelope(c, fmt.Sprintf("%s task %s (%s); oriented and ready to investigate", verb, c.ID, c.Goal), facts, warnings, next)
	return env, nil
}

func healthNote(reps []adapters.HealthReport) string {
	up, down := 0, 0
	for _, r := range reps {
		if r.Available {
			up++
		} else {
			down++
		}
	}
	return fmt.Sprintf("tool health: %d available, %d unavailable", up, down)
}

func normalizeMode(mode domain.Mode) (domain.Mode, bool) {
	if mode == "" {
		return domain.ModeChange, true
	}
	normalized := domain.Mode(strings.ToLower(strings.TrimSpace(string(mode))))
	return normalized, normalized.Valid()
}

// normalizeRisk canonicalizes the risk band to lowercase low|medium|high so
// downstream risk-escalation comparisons are robust to "--risk HIGH"
// or stray whitespace. Empty defaults to medium; unknown values are rejected.
func normalizeRisk(risk string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(risk))
	if normalized == "" {
		return "medium", true
	}
	switch normalized {
	case "low", "medium", "high":
		return normalized, true
	default:
		return "", false
	}
}

func normalizeSurfaces(surfaces []domain.Surface) ([]domain.Surface, error) {
	if len(surfaces) == 0 {
		return []domain.Surface{domain.SurfaceCode}, nil
	}
	out := make([]domain.Surface, 0, len(surfaces))
	seen := make(map[domain.Surface]bool, len(surfaces))
	for _, surface := range surfaces {
		normalized := domain.Surface(strings.ToLower(strings.TrimSpace(string(surface))))
		if !normalized.Valid() {
			return nil, fmt.Errorf("surface must be one of: code, browser, terminal, artifact, secret (got %q)", surface)
		}
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	return out, nil
}

func firstNonEmptyStr(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func joinStr(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
