package kernel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
)

// StartInput parameterizes StartTask (SPEC §10.2 cortex_start_task).
type StartInput struct {
	Goal     string
	Mode     domain.Mode
	Surfaces []domain.Surface
	Risk     string
	BaseRef  string // diff base for a review task (empty = working-tree diff)
}

// StartTask creates a case file and performs lightweight orientation: it reads
// git identity and probes tool health, then advances the task to investigating
// (SPEC §6.1 new → orienting → investigating).
func (k *Kernel) StartTask(ctx context.Context, in StartInput) (domain.Envelope, error) {
	if in.Goal == "" {
		return errEnvelope("", "a goal is required to start a task"), nil
	}
	mode := in.Mode
	if mode == "" {
		mode = domain.ModeChange
	}
	c := &domain.CaseFile{
		SchemaVersion: domain.SchemaVersion,
		ID:            ids.New("task"),
		CreatedAt:     k.now().UTC(),
		Goal:          in.Goal,
		Mode:          mode,
		Status:        domain.PhaseNew,
		Risk:          normalizeRisk(in.Risk),
		Surfaces:      defaultSurfaces(in.Surfaces),
		Workspace:     domain.Workspace{Root: k.cfg.Workspace, Repository: filepath.Base(k.cfg.Workspace), BaseRef: in.BaseRef},
	}

	// Persist the case skeleton FIRST. Appending to any ledger — phases.jsonl via
	// the transition below, or evidence via stampEvidence — creates the task
	// directory, so Create must run before them or it would see the directory
	// already present and refuse ("case already exists").
	if err := k.store.Create(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	// new → orienting: a goal and workspace exist.
	if err := k.transition(c, domain.PhaseOrienting); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
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
			if ev, err := k.stampEvidence(c.ID, "git", adapters.Fact{Kind: "code_location", Claim: claim, Confidence: "high"}); err == nil {
				facts = append(facts, ev)
			}
		} else {
			warnings = append(warnings, "workspace is not a git repository — scope-drift detection and diff review are limited")
		}
	}

	// Tool health snapshot (SPEC §6.2 orienting precondition).
	health := k.reg.Health(ctx)
	var down []string
	for _, h := range health {
		if !h.Available {
			down = append(down, h.Tool)
		}
	}
	c.Notes = append(c.Notes, healthNote(health))
	if len(down) > 0 {
		warnings = append(warnings, "tools unavailable: "+joinStr(down, ", ")+" — verification on their surfaces will be blocked")
	}

	// Cross-case disproof recall (SPEC §15.4): surface prior related cases as
	// low-confidence orientation so a weak model reads prior disproofs before
	// re-deriving a theory. Best-effort — a missing veclite is warn-once.
	prior, recallWarn, nPrior := k.recallPriorCases(ctx, c, c.Goal, 5)
	facts = append(facts, prior...)
	if recallWarn != "" {
		warnings = append(warnings, recallWarn)
	}

	// orienting → investigating: identity and tool health are known.
	if err := k.transition(c, domain.PhaseInvestigating); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}

	next := []string{
		"cortex investigate — discover by meaning, then resolve structure",
		"treat search output as candidates, not proof",
	}
	if nPrior > 0 {
		next = append([]string{fmt.Sprintf("%d prior related case(s) recalled — read before re-deriving a theory", nPrior)}, next...)
	}
	env := k.envelope(c, fmt.Sprintf("started task %s (%s); oriented and ready to investigate", c.ID, c.Goal), facts, warnings, next)
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

func defaultSurfaces(s []domain.Surface) []domain.Surface {
	if len(s) == 0 {
		return []domain.Surface{domain.SurfaceCode}
	}
	return s
}

// normalizeRisk canonicalizes the risk band to lowercase low|medium|high so
// downstream comparisons (e.g. the §13.3 escalation) are robust to "--risk HIGH"
// or stray whitespace. An unrecognized value defaults to medium.
func normalizeRisk(risk string) string {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "low":
		return "low"
	case "high":
		return "high"
	default:
		return "medium"
	}
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
