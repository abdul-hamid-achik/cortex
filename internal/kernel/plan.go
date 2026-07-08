package kernel

import (
	"fmt"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
)

// PlanInput parameterizes Plan (SPEC §10.2 cortex_plan). It is a planning gate,
// not a code generator.
type PlanInput struct {
	TaskID         string
	Hypotheses     []HypothesisInput
	ChangeBoundary domain.ChangeBoundary
	Verification   []string
	Uncertainty    string
	// TimeoutOverrides maps a tool name to a per-task timeout (SPEC §17.2).
	TimeoutOverrides map[string]string
}

// HypothesisInput is the model-facing hypothesis shape (disproveBy is free text
// or a structured contract; either satisfies the disproof requirement).
type HypothesisInput struct {
	Statement  string
	Supports   []string
	Confidence string
	DisproveBy string
}

// Plan stores hypotheses, the change boundary, and the verification plan, then
// gates the transition into a changing/verifying-ready state. It rejects any
// plan whose hypotheses lack a disproof path (SPEC §6.3 #1, §13.1).
func (k *Kernel) Plan(in PlanInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if c.Status != domain.PhaseInvestigating && c.Status != domain.PhasePlanned {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot plan in phase %q; investigate first", c.Status)), nil
	}
	if len(in.Hypotheses) == 0 {
		return errEnvelope(in.TaskID, "a plan needs at least one hypothesis with a disproof path"), nil
	}

	hyps := make([]domain.Hypothesis, 0, len(in.Hypotheses))
	for _, h := range in.Hypotheses {
		hyp := domain.Hypothesis{
			ID:         ids.New("hyp"),
			Statement:  h.Statement,
			Supports:   h.Supports,
			Confidence: mapConfidence(firstNonEmptyStr(h.Confidence, "low")),
			DisproveBy: domain.Disproof{Note: h.DisproveBy},
			Status:     domain.HypActive,
		}
		if err := hyp.Validate(); err != nil {
			// A rejected plan preserves the investigating phase so the model can
			// supply a disproof path and retry (SPEC acceptance: plan rejects
			// plans with no disproof path).
			return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
		}
		hyps = append(hyps, hyp)
	}

	// A change task must declare a boundary before it can mutate (SPEC §13.1).
	if c.Mode == domain.ModeChange && !in.ChangeBoundary.Declared() {
		return errEnvelope(in.TaskID, "plan rejected: a change task must declare a change boundary (files and/or symbols)"), nil
	}

	verification := in.Verification
	if len(verification) == 0 {
		verification = defaultVerification(c.Surfaces)
	}

	plan := domain.Plan{
		Hypotheses:           hyps,
		ChangeBoundary:       in.ChangeBoundary,
		VerificationRequired: verification,
		Uncertainty:          in.Uncertainty,
	}
	if err := plan.Validate(); err != nil {
		return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
	}

	// Persist plan artifacts and update the case.
	if err := k.store.SavePlan(c.ID, plan); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	if err := k.store.SaveHypotheses(c.ID, hyps); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	c.ChangeBoundary = in.ChangeBoundary
	c.VerificationRequired = verification
	c.TimeoutOverrides = in.TimeoutOverrides
	// investigating → planned (a hypothesis + disproof + verification plan exist).
	if err := k.transition(c, domain.PhasePlanned); err != nil {
		return errEnvelope(c.ID, err.Error()), nil
	}
	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	env := domain.Envelope{
		OK:     true,
		TaskID: c.ID,
		Phase:  c.Status,
		Summary: fmt.Sprintf("plan accepted: %s, boundary of %s, %s required",
			pluralizeGeneric(len(hyps), "hypothesis", "hypotheses"),
			boundarySummary(in.ChangeBoundary),
			pluralizeGeneric(len(verification), "verifier", "verifiers")),
		NextActions: []string{
			"make your edits within the declared boundary — expand the plan if scope changes",
			"cortex verify — run the required verifiers and check for scope drift",
		},
		RawAvailable: false,
	}
	// §13.1: a change task should have evidence supporting each hypothesis
	// before it enters changing. This is surfaced as a warning (not a hard
	// gate) so a hypothesis can be recorded before formal evidence exists, but
	// the gap is visible to the model.
	if c.Mode == domain.ModeChange {
		for _, h := range hyps {
			if len(h.Supports) == 0 {
				env.Warnings = append(env.Warnings, fmt.Sprintf("hypothesis %q has no supporting evidence — investigate to gather evidence before changing (SPEC §13.1)", h.ID))
			}
		}
	}
	for _, h := range hyps {
		env.Hypotheses = append(env.Hypotheses, domain.ToHypView(h))
	}
	return env, nil
}

// defaultVerification derives a verifier list from the task's surfaces when the
// model doesn't supply one (SPEC §14.1 claim-to-proof mapping).
func defaultVerification(surfaces []domain.Surface) []string {
	out := []string{"codemap_review"}
	for _, s := range surfaces {
		switch s {
		case domain.SurfaceBrowser:
			out = append(out, "cairntrace_flow")
		case domain.SurfaceTerminal:
			out = append(out, "glyphrun_flow")
		case domain.SurfaceArtifact:
			out = append(out, "fcheap_artifact")
		}
	}
	return dedupeStr(out)
}

func boundarySummary(b domain.ChangeBoundary) string {
	if !b.Declared() {
		return "no files (investigation only)"
	}
	return fmt.Sprintf("%d file(s) / %d symbol(s)", len(b.Files), len(b.Symbols))
}

func pluralizeGeneric(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
